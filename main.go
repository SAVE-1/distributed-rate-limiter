package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// https://www.hellointerview.com/learn/system-design/problem-breakdowns/distributed-rate-limiter

// A rate limiter is an infrastructure component that other services call
// to check if a request should be allowed.

var (
	version = "0.1"
	// simplistic internal cache for now
	cache              = map[string]RateLimiterEntry{}
	fixedWindowCache   = map[string]FixedWindowEntry{}
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

	r := gin.Default()
	r.Use(ratelimiterMiddleware())

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})

	r.Any("/*", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})

	r.Run()
}

func hasAMinutePassed(t time.Time) bool {
	timeNowInUnix := time.Now().Unix()
	timeThenInUnix := t.Unix()
	minuteInUnix := 60
	return (timeNowInUnix - timeThenInUnix) >= int64(minuteInUnix)
}

func ratelimiterMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.Request.RemoteAddr
		var cacheRequest FixedWindowEntry
		cacheRequest, ok := fixedWindowCache[ip]

		if !ok {
			cacheRequest = NewFixedWindowEntry(ip)
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
		fixedWindowCache[ip] = cacheRequest

		c.Next()
	}
}
