package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/dgraph-io/ristretto/v2"
	"github.com/gin-gonic/gin"
)

// https://www.hellointerview.com/learn/system-design/problem-breakdowns/distributed-rate-limiter

// A rate limiter is an infrastructure component that other services call
// to check if a request should be allowed.

var (
	version = "0.1"
	// simplistic internal cache for now
	cache              *ristretto.Cache[string, FixedWindowEntry]
	requestsUntilLimit = 20
	requestBaseTTL     = 1 * time.Minute
)

type RateLimiterEntry struct {
	Ip           string
	FirstHit     time.Time
	HitCount     int
	RatelimitTTL time.Duration
}

type FixedWindowEntry struct {
	Ip       string
	FirstHit time.Time
	HitCount int
}

// I'm not using the heap here, as this in my understanding quicker computationally
func NewFixedWindowEntry(ip string) FixedWindowEntry {
	return FixedWindowEntry{
		Ip:       ip,
		FirstHit: time.Now(),
		HitCount: 0,
	}
}

func main() {
	fmt.Println("Distributed rate limiter, version ", version)

	cache, err := ristretto.NewCache(&ristretto.Config[string, FixedWindowEntry]{
		NumCounters: 1e7,     // number of keys to track frequency of (10M).
		MaxCost:     1 << 30, // maximum cost of cache (1GB).
		BufferItems: 64,      // number of keys per Get buffer.
	})
	if err != nil {
		panic(err)
	}
	defer cache.Close()

	r := gin.Default()
	// r.Use(ratelimiterMiddleware())

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

	r.POST("/isrequestallowed", isRequestAllowed)

	r.Run()
}

type IncomingMessage struct {
	ClientId int
	RulesId  int
}

// isRequestAllowed(clientId, rulesId) -> {passes: boolean, remaining: number, resetTime: timestamp}
func isRequestAllowed(c *gin.Context) {
	var message IncomingMessage
	err := c.ShouldBindJSON(&message)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing either 'client id' or 'rules id'"})
	}

	ip := c.Request.RemoteAddr
	var cacheRequest FixedWindowEntry
	// get value from cache
	_, found := cache.Get(ip)
	if !found {
		panic("missing value")
	}

	requestCount := cacheRequest.HitCount
	requestLastWindow := cacheRequest.FirstHit

	if hasAMinutePassed(requestLastWindow) {
		requestCount = 0
		requestLastWindow = time.Now()
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

}

func hasAMinutePassed(t time.Time) bool {
	timeNowInUnix := time.Now().Unix()
	timeThenInUnix := t.Unix()
	minuteInUnix := 60
	return (timeNowInUnix - timeThenInUnix) >= int64(minuteInUnix)
}

// func ratelimiterMiddleware() gin.HandlerFunc {
// 	return func(c *gin.Context) {

// 		var message IncomingMessage
// 		err := c.ShouldBindJSON(&message)

// 		if err != nil {
// 			c.JSON(http.StatusBadRequest, gin.H{})
// 		}

// 		c.JSON(http.StatusOK, gin.H{
// 			"passes":    true,
// 			"remaining": 0,
// 			"resetTime": time.Now(),
// 		})

// 		ip := c.Request.RemoteAddr
// 		var cacheRequest FixedWindowEntry
// 		value, found := cache.Get("key")

// 		if !ok {
// 			cacheRequest = NewFixedWindowEntry(ip)
// 		}

// 		requestCount := cacheRequest.HitCount
// 		requestLastWindow := cacheRequest.FirstHit

// 		if hasAMinutePassed(requestLastWindow) {
// 			requestCount = 0
// 			requestLastWindow = time.Now()
// 		} else if requestsUntilLimit <= requestCount {
// 			// should add some x-headers to explain I guess
// 			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests"})
// 		}

// 		cacheRequest.FirstHit = requestLastWindow
// 		cacheRequest.HitCount = requestCount
// 		fixedWindowCache[ip] = cacheRequest

// 		c.Next()
// 	}
// }
