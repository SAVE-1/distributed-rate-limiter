package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/SAVE-1/distributed-rate-limiter/internal"
	"github.com/dgraph-io/ristretto/v2"
	"github.com/gin-gonic/gin"
)

// https://www.hellointerview.com/learn/system-design/problem-breakdowns/distributed-rate-limiter

// A rate limiter is an infrastructure component that other services call
// to check if a request should be allowed.

/*

LIMITATIONS:
	for now, only allowed global window is 1min

FOR NOW:
	if the REDIS cache instance is down,
	the internal cache will be updated, but once REDIS is back up again,
	the REDIS cache WILL NOT BE updated against the internal cache, but I suppose it is okay'ish in this case
	as the data is not mission critical, and the data velocity should be pretty high as well

	it does mean the rate limiting -rate is going to be somewhat lower in some cases though,
	which is bad for the overall system, but maybe I'll just live with it for now

LIMITATIONS
	For now, the only supported global window is 1 minute.

CURRENT BEHAVIOR / KNOWN ISSUES
	If the Redis cache instance goes down:
	– The in-memory (internal) cache continues to be updated normally.
	– Once Redis comes back online, it will not be automatically repopulated from the in-memory cache.
	– This is acceptable for the moment because the data is not mission-critical and has high churn/velocity anyway.

	This does mean that, during a Redis outage + recovery, the effective rate-limit enforcement may be slightly looser than intended in some cases.
	– This is suboptimal for the overall system, but I’ll live with it for now.

*/

var (
	version            = "0.2"
	cache              *ristretto.Cache[string, internal.RedisEntry]
	settingRequestsUntilLimit int64 = 20
	settingRequestBaseTTL           = 1 * time.Minute

	// settings
	settingAllowStartupWithoutRedis = false
	settingGlobalWindow             = "1min"
)

type FixedWindowEntry struct {
	Ip       string
	FirstHit time.Time
	HitCount int
}

func main() {
	fmt.Println("Distributed rate limiter, version ", version)

	if settingGlobalWindow != "1min" {
		fmt.Println("configuration error, global window must be 1min for now")
		return
	}

	address := "127.0.0.1:6379"
	username := "default"
	password := "mypassword"

	redisConnectionError := internal.InitRedis(address, username, password)

	if redisConnectionError != nil {
		fmt.Println(redisConnectionError)
	} else {
		fmt.Println("REDIS connection succeed")
	}

	if settingAllowStartupWithoutRedis {
		// stop the server altogether
		fmt.Println("Stopping server")
		return
	}

	cache, ristrettoInitError := ristretto.NewCache(&ristretto.Config[string, internal.RedisEntry]{
		NumCounters: 1e7,     // number of keys to track frequency of (10M).
		MaxCost:     1 << 30, // maximum cost of cache (1GB).
		BufferItems: 64,      // number of keys per Get buffer.
	})

	// this is fatal, internal cache is required in all cases
	if ristrettoInitError != nil {
		fmt.Println("Unable to init internal cache:", ristrettoInitError)
		return
	}

	defer cache.Close()

	r := gin.Default()

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message":      "feelin' fine!",
			"health":       "healthy",
			"health-level": 100,
			"code":         239, // a custom code, mostly to have it, and as a placeholder I guess, no biggie
		})
	})

	r.POST("/ratelimit", isRequestAllowed)

	r.Run()
}

// message from the web client
type IncomingMessage struct {
	ClientId string `json:"ClientId" binding:"required"`
	RulesId  string `json:"RulesId" binding:"required"`
}

func (i IncomingMessage) String() string {
	return fmt.Sprintf("client-id: %s, rules-id: %s", i.ClientId, i.RulesId)
}

// isRequestAllowed(clientId, rulesId) -> {passes: boolean, remaining: number, resetTime: timestamp}
func isRequestAllowed(c *gin.Context) {
	var message IncomingMessage

	if err := c.ShouldBindJSON(&message); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing either 'client id' or 'rules id'"})
	}

	fmt.Println(message)

	ip := extractClientIpWithoutPort(c)

	fmt.Println("connection ip:", ip)

	requestUserHash := fmt.Sprintf("ratelimit:%s", ip)

	user, redisError := internal.GetHashFromRedis(requestUserHash)

	if redisError != nil { // error with REDIS
		fmt.Println("error fetching from redis")
		c.JSON(http.StatusInternalServerError, gin.H{"Error": redisError})
		return
	} else if user != (internal.RedisEntry{}) { // no user found
		fmt.Println("Hash does not exist")

	 	entry := internal.RedisEntry{
			HitCount: 1,
			FirstHit: time.Now().Unix(),
		}

		fieldSetCount, err := internal.AddHashToRedis(requestUserHash, entry)

		if err != nil {
			fmt.Println("REDIS error:", err)
		} else {
			fmt.Printf("REDIS HSet success: %d fields set", fieldSetCount)
		}

		cache.Set(requestUserHash, entry, 1)
		// wait for value to pass through buffers
  		cache.Wait()

		// respond to request
		// headers:
		// RateLimit-Limit
		// RateLimit-Remaining == 
		// RateLimit-Reset == 
		// c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")

		c.Writer.Header().Set("X-RateLimit-Remaining", "true")
		c.Writer.Header().Set("X-RateLimit-Reset", "true")

	} else { // user found
		user.HitCount++
		
		/* 
			{
				passes: boolean, 
				remaining: number, 
				resetTime: timestamp
			}
		*/

		requestLastWindow := user.FirstHit
		if hasAMinutePassed(requestLastWindow) {
			user.HitCount = 0
			requestLastWindow = time.Now().Unix()
		} else if settingRequestsUntilLimit <= user.HitCount {
			// should add some x-headers to explain I guess
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests"})
		}

		internal.AddHashToRedis(requestUserHash, user)
		cache.Set(requestUserHash, user, 1)
		// wait for value to pass through buffers
  		cache.Wait()

		// respond to request

	// 	HTTP/2 429 Too Many Requests
	// Content-Type: application/json
		
	// {
	//   "type": "https://example.com/probs/limit-exceeded",
	//   "title": "Rate limit exceeded",
	//   "detail": "Too many requests in a given amount of time, please try again later."
	// }

	}
}

func hasAMinutePassed(t int64) bool {
	timeNowInUnix := time.Now().Unix()
	minuteInUnix := 60
	return (timeNowInUnix - t) >= int64(minuteInUnix)
}

func extractClientIpWithoutPort(c *gin.Context) string {
	i := strings.Index(c.Request.RemoteAddr, ":")
	return c.Request.RemoteAddr[:i]
}
