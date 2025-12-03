package ratelimiter

import (
	"context"
	"errors"
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

var (
	version        = "0.4"
	ristrettoCache *ristretto.Cache[string, internal.RedisEntry]
	globalSettings = Settings{
		AllowStartupWithoutRedis: false,
		Period:                   1 * time.Minute,
		Limit:                    100,
	}
)

type Settings struct {
	AllowStartupWithoutRedis bool
	Period                   time.Duration
	Limit                    int64
}

type FixedWindowEntry struct {
	Ip       string
	FirstHit time.Time
	HitCount int
}

type RateLimiterConfiguration struct {
	RedisAddress             string
	RedisUsername            string
	RedisPassword            string
	Period                   time.Duration
	Limit                    int64
	AllowStartupWithoutRedis bool
}

func Start(ratelimiterConfig RateLimiterConfiguration) error {
	fmt.Println("Distributed rate limiter, version", version)

	if ratelimiterConfig.Period != 0 {
		globalSettings.Period = ratelimiterConfig.Period
	}

	if ratelimiterConfig.Limit != 0 {
		globalSettings.Limit = ratelimiterConfig.Limit
	}

	if ratelimiterConfig.AllowStartupWithoutRedis != false {
		fmt.Println("hep")
		globalSettings.AllowStartupWithoutRedis = ratelimiterConfig.AllowStartupWithoutRedis
	}

	fmt.Println("Allow startupt without REDIS:", globalSettings.AllowStartupWithoutRedis)
	fmt.Println("Request window:", globalSettings.Period)
	fmt.Println("Requests until limit:", globalSettings.Limit)

	config := zap.Config{
		Level:            zap.NewAtomicLevelAt(zap.InfoLevel),
		Development:      false,
		Encoding:         "json",
		EncoderConfig:    zap.NewDevelopmentEncoderConfig(),
		OutputPaths:      []string{"stdout", "rate-limiter.log"},
		ErrorOutputPaths: []string{"stderr", "rate-limiter.log"},
	}

	logger, err := config.Build()
	if err != nil {
		panic(err)
	}

	defer logger.Sync()

	logger.Info("Custom logger initialized")

	address := ratelimiterConfig.RedisAddress
	username := ratelimiterConfig.RedisUsername
	password := ratelimiterConfig.RedisPassword

	redisConnection := internal.NewRedisConnection(address, username, password, logger)

	client, redisConnectionError := internal.OpenRedisConnection(redisConnection)

	if redisConnectionError != nil {
		logger.Error("Error connecting to redis", zap.Error(redisConnectionError))
		if !globalSettings.AllowStartupWithoutRedis {
			// stop the server altogether
			logger.Error("Shutting down rate limiter, REDIS is required")
			return errors.Join(errors.New("Startup not allowed without REDIS"), redisConnectionError)
		}
	} else {
		logger.Info("Connected to REDIS instance successfully")
	}

	redisConnection.RedisClient = client

	h := NewHandler(logger, *redisConnection)

	if globalSettings.Period != 1*time.Minute {
		logger.Error("Configuration error - global window must be 1min for now")
		return errors.New("Startup not allowed with a limit other than 1min")
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
		return ristrettoInitError
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
	return nil
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

		if hasAMinutePassed(user.FirstHit) { // ok
			fmt.Println("1st if -- hasAMinutePassed")
			user.Reset()

			h.RedisConnection.AddHashToRedis(requestUserHash, user, globalSettings.Period)
			t := timeUntil(user.FirstHit)

			setRatelimitSpecificHeaders(c, user, globalSettings)
			c.JSON(http.StatusOK, gin.H{
				"passes":     true,
				"reset_unix": t.Unix(),
				"reset_iso":  t.Format(time.RFC3339),
				"limit":      globalSettings.Limit,
				"remaining":  globalSettings.Limit - user.HitCount,
			})
		} else if globalSettings.Limit < user.HitCount { // too many requests
			fmt.Println("2nd if -- hasAMinutePassed")
			t := timeUntil(user.FirstHit)

			setRatelimitSpecificHeaders(c, user, globalSettings)
			c.JSON(http.StatusTooManyRequests, gin.H{
				"passes":     false,
				"reset_unix": t.Unix(),
				"reset_iso":  t.Format(time.RFC3339),
				"limit":      globalSettings.Limit,
				"remaining":  0, // 0, we don't want to show minus counts
			})

			h.RedisConnection.AddHashToRedis(requestUserHash, user, globalSettings.Period)
		} else {
			fmt.Println("3rd if -- hasAMinutePassed")
			t := timeUntil(user.FirstHit)
			setRatelimitSpecificHeaders(c, user, globalSettings)
			c.JSON(http.StatusOK, gin.H{
				"passes":     true,
				"reset_unix": t.Unix(),
				"reset_iso":  t.Format(time.RFC3339),
				"limit":      globalSettings.Limit,
				"remaining":  globalSettings.Limit - user.HitCount,
			})
			h.RedisConnection.AddHashToRedis(requestUserHash, user, globalSettings.Period)
		}
	} else { // no user found
		fmt.Println("Hash does not exist")
		_time := time.Now()
		user = internal.RedisEntry{
			HitCount: 1,
			FirstHit: _time.Unix(),
		}

		err := h.RedisConnection.AddHashToRedis(requestUserHash, user, globalSettings.Period)

		if err != nil {
			fmt.Println("REDIS error:", err)
		} else {
			fmt.Println("REDIS HSet success")
		}

		setRatelimitSpecificHeaders(c, user, globalSettings)
		t := timeUntil(user.FirstHit)
		c.JSON(http.StatusOK, gin.H{
			"passes":     true,
			"reset_unix": t.Unix(),
			"reset_iso":  t.Format(time.RFC3339),
			"limit":      globalSettings.Limit,
			"remaining":  globalSettings.Limit - user.HitCount,
		})
	}

	ristrettoCache.SetWithTTL(requestUserHash, user, useRistrettoCostCalc, globalSettings.Period)
	ristrettoCache.Wait()
}

func timeUntil(t int64) time.Time {
	return time.Unix(t+60, 0)
}

func setRatelimitSpecificHeaders(c *gin.Context, user internal.RedisEntry, settings Settings) {
	var remaining int64 = -1
	if settings.Limit-user.HitCount < 0 {
		remaining = 0
	} else {
		remaining = settings.Limit - user.HitCount
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.Header().Set("RateLimit-Remaining", strconv.FormatInt(remaining, 10))
	c.Writer.Header().Set("RateLimit-Reset", strconv.FormatInt(secondsUntilRatelimitReset(user.FirstHit, globalSettings.Period), 10))
	c.Writer.Header().Set("RateLimit-Limit", strconv.FormatInt(globalSettings.Limit, 10))
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
