package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/SAVE-1/distributed-rate-limiter/internal"
	"github.com/dgraph-io/ristretto/v2"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

/*

LIMITATIONS:
	for now, only allowed global window is 1min

FOR NOW:
	if the REDIS cache instance is down,
	the internal cache will be updated, but once REDIS is back up again,
	the REDIS cache will not be updated against the internal cache, but I suppose it is okay'ish in this case
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
	version        = "0.3"
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
	fmt.Println("Distributed rate limiter, version ", version)

	config := zap.Config{
		Level:            zap.NewAtomicLevelAt(zap.InfoLevel),
		Development:      false,
		Encoding:         "json",
		EncoderConfig:    zap.NewDevelopmentEncoderConfig(),
		OutputPaths:      []string{"stdout", "rate-limiter.log"},
		ErrorOutputPaths: []string{"stderr"},
	}

	logger, err := config.Build()
	if err != nil {
		panic(err)
	}

	defer logger.Sync()

	logger.Info("Custom logger initialized")

	address := "127.0.0.1:6379"
	username := "default"
	password := "mypassword"

	redisConnection := internal.NewRedisConnection(address, username, password, logger)

	client, redisConnectionError := internal.OpenRedisConnection(redisConnection)

	if redisConnectionError != nil {
		logger.Error("Error connecting to redis", zap.Error(redisConnectionError))
		return
	} else {
		logger.Info("Connected to REDIS instance successfully")
	}

	redisConnection.RedisClient = client

	if globalSettings.AllowStartupWithoutRedis {
		// stop the server altogether
		logger.Error("Shutting down rate limiter, REDIS is required")
		return
	}

	h := NewHandler(logger, *redisConnection)

	if globalSettings.Window != 1*time.Minute {
		logger.Error("Configuration error - global window must be 1min for now")
		return
	}

	cache, ristrettoInitError := ristretto.NewCache(&ristretto.Config[string, internal.RedisEntry]{
		NumCounters: 1e7,     // number of keys to track frequency of (10M).
		MaxCost:     1 << 30, // maximum cost of cache (1GB).
		BufferItems: 64,      // number of keys per Get buffer.
		Cost:        redisConnection.GetRedisEntryCostFunction(),
	})

	// this is fatal, internal cache is required in all cases
	if ristrettoInitError != nil {
		logger.Error("Internal error, unable to init internal cache, shutting down rate limiter", zap.Error(ristrettoInitError))
		return
	}

	defer cache.Close()

	router := gin.Default()

	{
		v1 := router.Group("/v1")

		v1.GET("/ping", h.ping)

		v1.GET("/health", h.health)

		v1.POST("/ratelimit", h.isRequestAllowed)
	}

	server := &http.Server{
		Addr:    ":8080",
		Handler: router,
	}

	go func() {
		log.Println("Starting server on :8080")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Listen error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	// kill (no params) by default sends syscall.SIGTERM
	// kill -2 is syscall.SIGINT
	// kill -9 is syscall.SIGKILL but can't be caught, so don't need add it
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Fatal("Forced shutdown", zap.Error(err))
	}

	logger.Info("Server exited gracefully")
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

type Handler struct {
	Logger          *zap.Logger
	RedisConnection internal.RedisConnection
}

func NewHandler(logger *zap.Logger, conn internal.RedisConnection) *Handler {
	return &Handler{
		Logger:          logger,
		RedisConnection: conn,
	}
}

func (h *Handler) ping(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"message": "pong",
	})
}

func (h *Handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"message":      "feelin' fine!",
		"health":       "healthy",
		"health-level": 100,
		"code":         239, // a custom code, mostly to have it, and as a placeholder I guess, no biggie
	})
}

// isRequestAllowed(clientId, rulesId) -> {passes: boolean, remaining: number, resetTime: timestamp}
func (h *Handler) isRequestAllowed(c *gin.Context) {
	const useRistrettoCostCalc int64 = 0

	var message IncomingMessage

	userId := ""

	if err := c.ShouldBindJSON(&message); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing either 'client id' or 'rules id'"})
		return
	}

	userId = message.ClientId

	fmt.Println(message)

	fmt.Println("user id:", userId)

	requestUserHash := "ratelimit:" + userId

	fmt.Println("requestUserHash: ", requestUserHash)

	user, redisError := h.RedisConnection.GetHashFromRedis(requestUserHash)

	if redisError != nil { // error with REDIS
		h.Logger.Error("error fetching from redis", zap.Error(redisError))
		c.JSON(http.StatusInternalServerError, gin.H{"Error": redisError})
		return
	} else if user.Ok() { // user found
		fmt.Println("user found")
		user.HitCount++

		fmt.Println("user", user)

		if hasAMinutePassed(user.FirstHit) {
			fmt.Println("1st if -- hasAMinutePassed")
			user.Reset()

			h.RedisConnection.AddHashToRedis(requestUserHash, user, globalSettings.Window)

			setRatelimitSpecificHeaders(c, user, globalSettings)
			c.JSON(http.StatusOK, gin.H{
				"title":  "Rate limit OK",
				"detail": "Rate limit OK.",
			})
		} else if globalSettings.RequestsUntilLimit < user.HitCount {
			fmt.Println("2nd if -- hasAMinutePassed")

			setRatelimitSpecificHeaders(c, user, globalSettings)
			c.JSON(http.StatusTooManyRequests, gin.H{
				"title":  "Rate limit exceeded",
				"detail": "Too many requests in a given amount of time, please try again later.",
			})

			h.RedisConnection.AddHashToRedis(requestUserHash, user, globalSettings.Window)
		} else {
			fmt.Println("3rd if -- hasAMinutePassed")

			setRatelimitSpecificHeaders(c, user, globalSettings)
			c.JSON(http.StatusOK, gin.H{
				"title":  "Rate limit ok",
				"detail": "Rate limit not exceeded",
			})
			h.RedisConnection.AddHashToRedis(requestUserHash, user, globalSettings.Window)
		}
	} else { // no user found
		fmt.Println("Hash does not exist")
		_time := time.Now()
		user = internal.RedisEntry{
			HitCount: 1,
			FirstHit: _time.Unix(),
		}

		err := h.RedisConnection.AddHashToRedis(requestUserHash, user, globalSettings.Window)

		if err != nil {
			fmt.Println("REDIS error:", err)
		} else {
			fmt.Println("REDIS HSet success")
		}

		setRatelimitSpecificHeaders(c, user, globalSettings)
		c.JSON(http.StatusOK, gin.H{
			"title":  "Rate limit ok",
			"detail": "Rate limit ok",
		})
	}

	ristrettoCache.SetWithTTL(requestUserHash, user, useRistrettoCostCalc, globalSettings.Window)
	ristrettoCache.Wait()
}

func setRatelimitSpecificHeaders(c *gin.Context, user internal.RedisEntry, settings Settings) {
	var remaining int64 = -1
	if settings.RequestsUntilLimit-user.HitCount < 0 {
		remaining = 0
	} else {
		remaining = settings.RequestsUntilLimit - user.HitCount
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.Header().Set("RateLimit-Remaining", strconv.FormatInt(remaining, 10))
	c.Writer.Header().Set("RateLimit-Reset", strconv.FormatInt(secondsUntilRatelimitReset(user.FirstHit, globalSettings.Window), 10))
	c.Writer.Header().Set("RateLimit-Limit", strconv.FormatInt(globalSettings.RequestsUntilLimit, 10))
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
	const millisInSecond int64 = 1000
	return (userFirstHitAsTime.Add(window).UnixMilli() - time.Now().UnixMilli()) / millisInSecond
}

func hasAMinutePassed(previousInUnix int64) bool {
	timeNowInUnix := time.Now().Unix()
	const minuteInUnix int64 = 60
	return (timeNowInUnix - previousInUnix) >= minuteInUnix
}

func extractClientIpWithoutPort(c *gin.Context) string {
	i := strings.Index(c.Request.RemoteAddr, ":")
	return c.Request.RemoteAddr[:i]
}
