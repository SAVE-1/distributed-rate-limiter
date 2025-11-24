package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"unsafe"

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
	version        = "0.2"
	ristrettoCache *ristretto.Cache[string, internal.RedisEntry]
	globalSettings = Settings{
		AllowStartupWithoutRedis: false,
		Window:                   1 * time.Minute,
		RequestsUntilLimit:       2,
	}
)

type Settings struct {
	AllowStartupWithoutRedis bool
	Window                   time.Duration
	RequestsUntilLimit       int64
}

type FixedWindowEntry struct {
	Ip       string
	FirstHit time.Time
	HitCount int
}

func main() {

	j := internal.RedisEntry{
		HitCount: 1,
		FirstHit: 1,
	}

	fmt.Println(int64(unsafe.Sizeof(j)))

	fmt.Println("Distributed rate limiter, version ", version)

	if globalSettings.Window != 1*time.Minute {
		fmt.Println("Configuration error, global window must be 1min for now")
		return
	}

	address := "127.0.0.1:6379"
	username := "default"
	password := "mypassword"

	redisConnectionError := internal.InitRedis(address, username, password)

	if redisConnectionError != nil {
		fmt.Println(redisConnectionError)
	} else {
		fmt.Println("REDIS connection success")
	}

	// defer fmt.Println(internal.CloseRedis())

	if globalSettings.AllowStartupWithoutRedis {
		// stop the server altogether
		fmt.Println("Powering down server")
		return
	}

	cache, ristrettoInitError := ristretto.NewCache(&ristretto.Config[string, internal.RedisEntry]{
		NumCounters: 1e7,     // number of keys to track frequency of (10M).
		MaxCost:     1 << 30, // maximum cost of cache (1GB).
		BufferItems: 64,      // number of keys per Get buffer.
		Cost: internal.GetRedisEntryCostFunction(),
	})

	// this is fatal, internal cache is required in all cases
	if ristrettoInitError != nil {
		fmt.Println("Fatal, unable to init internal cache:", ristrettoInitError)
		fmt.Println("Powering down server")
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

	// user-facing API
	r.Group("/v1")
	{
		r.POST("/ratelimit", isRequestAllowed)
	}

	r.Run()
}

// message from the web client
type IncomingMessage struct {
	ClientId string `json:"ClientId" binding:"required"`
	RulesId  string `json:"RulesId" binding:"required"`
}

func (i IncomingMessage) String() string {
	var b strings.Builder
    b.Grow(128) // preallocate enough

    b.WriteString("[ client-id: ")
    b.WriteString(i.ClientId)
    b.WriteString(", rules-id: ")
    b.WriteString(i.RulesId)
    b.WriteString(" ]")

	return b.String()
}

// isRequestAllowed(clientId, rulesId) -> {passes: boolean, remaining: number, resetTime: timestamp}
func isRequestAllowed(c *gin.Context) {
	var message IncomingMessage

	if err := c.ShouldBindJSON(&message); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing either 'client id' or 'rules id'"})
		return
	}

	fmt.Println(message)

	ip := extractClientIpWithoutPort(c)

	fmt.Println("connection ip:", ip)

	requestUserHash := "ratelimit:" + ip

	fmt.Println("requestUserHash: ", requestUserHash)

	user, redisError := internal.GetHashFromRedis(requestUserHash)

	if redisError != nil { // error with REDIS
		fmt.Println("error fetching from redis")
		c.JSON(http.StatusInternalServerError, gin.H{"Error": redisError})
		return
	} else if user.Ok() { // user found
		fmt.Println("1st if")
		fmt.Println("user found")
		user.HitCount++

		fmt.Println("user", user)

		if hasAMinutePassed(user.FirstHit) {
			user.HitCount = 1

			internal.AddHashToRedis(requestUserHash, user, globalSettings.Window)
			const cost int64 = 1
			ristrettoCache.SetWithTTL(requestUserHash, user, 0, globalSettings.Window)
			ristrettoCache.Wait()

			// respond to request
			c.Writer.Header().Set("RateLimit-Remaining", string(0)) // compiler is gonna optimize this to "0", but I'm keeping it as string(0) for consistencys sake
			c.Writer.Header().Set("RateLimit-Reset", string(secondsUntilRatelimitReset(user.FirstHit, globalSettings.Window)))
			c.Writer.Header().Set("RateLimit-Limit", string(globalSettings.RequestsUntilLimit))
			c.JSON(http.StatusOK, gin.H{
				"title":  "Rate limit OK",
				"detail": "Rate limit OK.",
			})

		} else if globalSettings.RequestsUntilLimit < user.HitCount {
			fmt.Println("2nd if")
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests"})
			c.Writer.Header().Set("RateLimit-Remaining", string(0)) // compiler is gonna optimize this to "0", but I'm keeping it as string(0) for consistencys sake
			c.Writer.Header().Set("RateLimit-Reset", string(secondsUntilRatelimitReset(user.FirstHit, globalSettings.Window)))
			c.Writer.Header().Set("RateLimit-Limit", string(globalSettings.RequestsUntilLimit))
			c.JSON(http.StatusOK, gin.H{
				"title":  "Rate limit exceeded",
				"detail": "Too many requests in a given amount of time, please try again later.",
			})

			internal.AddHashToRedis(requestUserHash, user, globalSettings.Window)
			ristrettoCache.SetWithTTL(requestUserHash, user, 0, globalSettings.Window)
			ristrettoCache.Wait()
		} else {
			fmt.Println("3rd if")
			internal.AddHashToRedis(requestUserHash, user, globalSettings.Window)
			ristrettoCache.SetWithTTL(requestUserHash, user, 0, globalSettings.Window)
			ristrettoCache.Wait()
		}
	} else { // no user found
		fmt.Println("Hash does not exist")
		_time := time.Now()
		entry := internal.RedisEntry{
			HitCount: 1,
			FirstHit: _time.Unix(),
		}

		err := internal.AddHashToRedis(requestUserHash, entry, globalSettings.Window)

		if err != nil {
			fmt.Println("REDIS error:", err)
		} else {
			fmt.Printf("REDIS HSet success")
		}

		ristrettoCache.SetWithTTL(requestUserHash, entry, 0, globalSettings.Window)
		ristrettoCache.Wait() // wait for value to pass through buffers

		// respond to request
		c.Writer.Header().Set("RateLimit-Remaining", string(globalSettings.RequestsUntilLimit-entry.HitCount))
		c.Writer.Header().Set("RateLimit-Reset", _time.Add(globalSettings.Window).String())
		c.Writer.Header().Set("RateLimit-Limit", string(globalSettings.RequestsUntilLimit))
		c.JSON(http.StatusOK, gin.H{
			"title":  "Rate limit ok",
			"detail": "Rate limit ok",
		})
	}
}

func secondsUntilRatelimitReset(userFirstHit int64, window time.Duration) int64 {
	/*
		example:
			12:59:00 <- user first hit
			12:59:30 <- user reached limit
			13:00:00 <- rate limit reset

		so:
			rateLimitTimer = 13:00:00
			userFirstHit = 12:59:00
			timeNow = 12:59:30
	*/

	userFirstHitAsTime := time.Unix(userFirstHit, 0)
	return (userFirstHitAsTime.Add(window).UnixMilli() - time.Now().UnixMilli()) / 1000
}

func hasAMinutePassed(previousInUnix int64) bool {
	timeNowInUnix := time.Now().Unix()
	var minuteInUnix int64 = 60
	return (timeNowInUnix - previousInUnix) >= minuteInUnix
}

func extractClientIpWithoutPort(c *gin.Context) string {
	i := strings.Index(c.Request.RemoteAddr, ":")
	return c.Request.RemoteAddr[:i]
}
