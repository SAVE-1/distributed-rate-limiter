package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/SAVE-1/distributed-rate-limiter/internal"
	"github.com/dgraph-io/ristretto/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// https://www.hellointerview.com/learn/system-design/problem-breakdowns/distributed-rate-limiter

// A rate limiter is an infrastructure component that other services call
// to check if a request should be allowed.

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
	var ctx = context.Background()

	rdb := redis.NewClient(&redis.Options{
		Addr:     "127.0.0.1:6379",
		Username: "default",    // use your Redis user. More info https://redis.io/docs/latest/operate/oss_and_stack/management/security/acl/
		Password: "mypassword", // use your Redis password
	})

	err := rdb.HSet(ctx, "user:1001", "name", "John", "age", "30").Err()
	if err != nil {
		panic(err)
	}

	val, err := rdb.HGetAll(ctx, "user:1003").Result()
	if err != nil {
		fmt.Println("user:1003 - not found")
	}

	fmt.Println("user:1003", val)

	rdb.Close()

	// ip := "127.0.0.1:899"
	// i := strings.Index(ip, ":")
	// fmt.Println(ip[:i])

	fmt.Println("Distributed rate limiter, version ", version)

	redisConnectionError := internal.InitRedis()

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

	requestUserHash := "ratelimit:1.1.1.1"

	user, doesHashExistError := internal.GetHashFromRedis(requestUserHash)

	if doesHashExistError != nil {
		fmt.Println("err")
	} else if user.Ok() {

	}

	cache, err := ristretto.NewCache(&ristretto.Config[string, internal.RedisEntry]{
		NumCounters: 1e7,     // number of keys to track frequency of (10M).
		MaxCost:     1 << 30, // maximum cost of cache (1GB).
		BufferItems: 64,      // number of keys per Get buffer.
	})

	// this is fatal, internal cache is required in all cases
	if err != nil {
		fmt.Println("Unable to init internal cache:", err)
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

	ip := getClientIpWithoutPort(c)

	fmt.Println("connection ip:", ip)

	requestUserHash := fmt.Sprintf("ratelimit:%s", ip)

	user, doesHashExistError := internal.GetHashFromRedis(requestUserHash)

	if doesHashExistError != nil {
		fmt.Println("error fetching from redis")
	} else if user != (internal.RedisEntry{}) {
		user.HitCount++
		
		/* 
			{
				passes: boolean, 
				remaining: number, 
				resetTime: timestamp
			}
		*/

		// requestLastWindow := v.FirstHit
		// if hasAMinutePassed(requestLastWindow) {
		// 	requestCount = 0
		// 	requestLastWindow = time.Now().Unix()
		// } else if settingRequestsUntilLimit <= requestCount {
		// 	// should add some x-headers to explain I guess
		// 	c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests"})
		// }

		passes := false

		c.JSON(http.StatusOK, gin.H{"passes": passes})
		internal.AddHashToRedis(requestUserHash, user)
	} else { // redis hit
		fmt.Println("Hash does not exist")

	 	entry := internal.RedisEntry{
			HitCount: 1,
			FirstHit: time.Now().Unix(),
			Window:   settingGlobalWindow,
		}

		fieldSetCount, err := internal.AddHashToRedis(requestUserHash, entry)

		if err != nil {
			fmt.Println("unable to set hash in redis:", err)
		} else {
			fmt.Println("fields set:", fieldSetCount)
		}

		cache.Set(requestUserHash, entry, 1)
		// wait for value to pass through buffers
  		cache.Wait()
	}
}

func hasAMinutePassed(t int64) bool {
	timeNowInUnix := time.Now().Unix()
	minuteInUnix := 60
	return (timeNowInUnix - t) >= int64(minuteInUnix)
}

func getClientIpWithoutPort(c *gin.Context) string {
	i := strings.Index(c.Request.RemoteAddr, ":")
	return c.Request.RemoteAddr[:i]
}
