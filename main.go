package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/SAVE-1/distributed-rate-limiter/internal"
	"github.com/dgraph-io/ristretto/v2"
	"github.com/gin-gonic/gin"
)

// https://www.hellointerview.com/learn/system-design/problem-breakdowns/distributed-rate-limiter

// A rate limiter is an infrastructure component that other services call
// to check if a request should be allowed.

var (
	version            = "0.2"
	cache              *ristretto.Cache[string, internal.RedisHit]
	requestsUntilLimit = 20
	requestBaseTTL     = 1 * time.Minute
)

type FixedWindowEntry struct {
	Ip       string
	FirstHit time.Time
	HitCount int
}

func main() {
	fmt.Println("Distributed rate limiter, version ", version)

	internal.InitRedis()

	cache, err := ristretto.NewCache(&ristretto.Config[string, internal.RedisHit]{
		NumCounters: 1e7,     // number of keys to track frequency of (10M).
		MaxCost:     1 << 30, // maximum cost of cache (1GB).
		BufferItems: 64,      // number of keys per Get buffer.
	})

	if err != nil {
		panic(err)
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

	ip := c.Request.RemoteAddr

	internal.AddHash(fmt.Sprintf("ratelimit:%s", ip), internal.RedisHit{
		HitCount: 1,
		FirstHit: time.Now().Unix(),
		Window:   "1min",
	})

	/*
		At this time, I'm going to use the naive implementation, where I fetch the user, update and send back

		At a later time I will do the LUA-REDIS optimization
	*/

	var cacheRequest internal.RedisHit
	// get value from cache
	user, found := cache.Get(ip)

	if found {
		fmt.Println(user)
		requestCount := cacheRequest.HitCount
		requestLastWindow := cacheRequest.FirstHit

		if hasAMinutePassed(requestLastWindow) {
			requestCount = 0
			requestLastWindow = time.Now().Unix()
		} else if requestsUntilLimit <= requestCount {
			// should add some x-headers to explain I guess
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests"})
		}

		cacheRequest.FirstHit = requestLastWindow
		cacheRequest.HitCount = requestCount

		// set a value with a cost of 1
		cache.Set(ip, cacheRequest, 1)

		// wait for value to pass through buffers
		cache.Wait()

		c.JSON(http.StatusOK, gin.H{
			"passes":    true,
			"remaining": 0,
			"resetTime": time.Now(),
		})

	} else {
		fmt.Println(fmt.Sprintf("ip not in cache: %s", ip))
	}
}

func hasAMinutePassed(t int64) bool {
	timeNowInUnix := time.Now().Unix()
	minuteInUnix := 60
	return (timeNowInUnix - t) >= int64(minuteInUnix)
}
