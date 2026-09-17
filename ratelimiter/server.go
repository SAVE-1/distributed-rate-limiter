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
	"go-micro.dev/v5/logger"
	"go.uber.org/zap"
)

var (
	version = "0.5"
)

func GetVersion() string {
	return version
}

const (
	Development = "dev"
	Production  = "prod"
)

type RateLimiterConfiguration struct {
	RedisAddress             string
	RedisUsername            string
	RedisPassword            string
	Period                   time.Duration
	Limit                    int64
	AllowStartupWithoutRedis bool
	DefaultAlgorithm         string
	Port                     int
	Mode                     string
}

func NewRatelimiter(ratelimiterConfig RateLimiterConfiguration) (*RatelimiterHandler, error) {
	var config zap.Config

	if ratelimiterConfig.Mode == Development {
		config = zap.Config{
			Level:            zap.NewAtomicLevelAt(zap.DebugLevel),
			Development:      true,
			Encoding:         "json",
			EncoderConfig:    zap.NewDevelopmentEncoderConfig(),
			OutputPaths:      []string{"stdout", "rate-limiter.log"},
			ErrorOutputPaths: []string{"stdout", "stderr", "rate-limiter.log"},
		}
	} else if ratelimiterConfig.Mode == Production {
		config = zap.Config{
			Level:            zap.NewAtomicLevelAt(zap.InfoLevel),
			Development:      false,
			Encoding:         "json",
			EncoderConfig:    zap.NewProductionEncoderConfig(),
			OutputPaths:      []string{"stdout", "rate-limiter.log"},
			ErrorOutputPaths: []string{"stdout", "stderr", "rate-limiter.log"},
		}
	} else {
		return nil, fmt.Errorf("invalid mode %q: must be %q or %q", ratelimiterConfig.Mode, Development, Production)
	}

	logger, err := config.Build()
	if err != nil {
		panic(err)
	}

	defer logger.Sync()

	address := ratelimiterConfig.RedisAddress
	username := ratelimiterConfig.RedisUsername
	password := ratelimiterConfig.RedisPassword

	redisConnection := internal.NewRedisConnection(address, username, password, logger)

	client, redisConnectionError := internal.OpenRedisConnection(redisConnection)

	if redisConnectionError != nil {
		// stop the server altogether
		if !ratelimiterConfig.AllowStartupWithoutRedis {
			return nil, errors.Join(redisConnectionError, errors.New("startup not allowed without Redis"))
		}
	} else {
		logger.Info("Connected to REDIS instance successfully")
	}

	redisConnection.RedisClient = client

	serverConfig := NewHandlerConfig(ratelimiterConfig.DefaultAlgorithm, ratelimiterConfig.Period)

	h := NewRatelimiterHandler(logger, *redisConnection, serverConfig)

	if h.Config.Period != 1*time.Minute {
		logger.Error("Configuration error - global window must be 1min for now")
		return nil, errors.New("Startup not allowed with a limit other than 1min")
	}

	h.Config.Limit = ratelimiterConfig.Limit

	logger.Info("Custom logger initialized")
	logger.Info("Distributed rate limiter, version", zap.String("version", version))
	logger.Info("Allow startupt without REDIS:", zap.Bool("allow startup without redis", h.Config.AllowStartupWithoutRedis))
	logger.Info("Request window:", zap.String("request window", h.Config.Period.String()))
	logger.Info("Requests until limit:", zap.Int64("global limit", h.Config.Limit))

	var ristrettoInitError error
	h.ristrettoCache, ristrettoInitError = ristretto.NewCache(&ristretto.Config[string, internal.RedisEntry]{
		NumCounters: 1e7,     // number of keys to track frequency of (10M).
		MaxCost:     1 << 30, // maximum cost of cache (1GB).
		BufferItems: 64,      // number of keys per Get buffer.
		Cost:        redisConnection.GetRedisEntryCostFunction(),
	})

	// this is fatal, internal cache is required in all cases
	if ristrettoInitError != nil {
		h.Logger.Error("Internal error, unable to init internal cache, shutting down rate limiter", zap.Error(ristrettoInitError))
		return nil, ristrettoInitError
	} else {
		h.Logger.Info("Internal cache started successfully")
	}

	defer h.ristrettoCache.Close()

	router := BuildRouter(h)
	server := &http.Server{
		// Addr:    ":" + ratelimiterConfig.Port fmt.Printf(":%d", ratelimiterConfig.Port),
		Addr:    fmt.Sprintf(":%d", ratelimiterConfig.Port),
		Handler: router,
	}

	h.httpThingy = server
	h.router = router

	return h, nil
}

func Start(ratelimiterConfig RateLimiterConfiguration) error {
	h, err := NewRatelimiter(ratelimiterConfig)

	if err != nil {
		return err
	}

	go func() {
		log.Println("Starting server on port", ratelimiterConfig.Port)
		if err := h.httpThingy.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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

	if err := h.httpThingy.Shutdown(ctx); err != nil {
		logger.Fatal("Forced shutdown", zap.Error(err))
	}

	logger.Info("Server exited gracefully")
	return nil
}

// message from the web client
type IncomingMessage struct {
	ClientId  string `json:"ClientId" binding:"required"`
	RulesId   string `json:"RulesId" binding:"required"`
	Algorithm string `json:"Algorithm" binding:"required"`
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

/*
	AllowStartupWithoutRedis: false,
	Period:                   1 * time.Minute,
	Limit:                    100,
*/

type HandlerConfig struct {
	DefaultAlgorithm         string
	Period                   time.Duration
	AllowStartupWithoutRedis bool
	Limit                    int64
}

func NewHandlerConfig(algo string, ttl time.Duration) *HandlerConfig {
	return &HandlerConfig{
		DefaultAlgorithm: algo,
		Period:           ttl,
	}
}

type RatelimiterHandler struct {
	Logger          *zap.Logger
	RedisConnection internal.RedisConnection
	ristrettoCache  *ristretto.Cache[string, internal.RedisEntry]
	Config          *HandlerConfig
	router          *gin.Engine
	httpThingy		*http.Server
}



func BuildRouter(h *RatelimiterHandler) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New() // use gin.New() in tests — gin.Default() adds a logger that spams stdout

	v1 := router.Group("/v1")
	v1.GET("/ping", h.ping)
	v1.GET("/health", h.health)
	v1.POST("/ratelimit", h.isRequestAllowed)

	return router
}

func NewRatelimiterHandler(logger *zap.Logger, conn internal.RedisConnection, h *HandlerConfig) *RatelimiterHandler {
	return &RatelimiterHandler{
		Logger:          logger,
		RedisConnection: conn,
		Config:          h,
	}
}

func (h *RatelimiterHandler) getFromCache(key string) (internal.RedisEntry, bool) {
  	value, found := h.ristrettoCache.Get(key)
  	if !found {
  	  return internal.RedisEntry{}, false
  	}

	return value, true
}

func (h *RatelimiterHandler) ping(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"message": "pong",
	})
}

func (h *RatelimiterHandler) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"message":      "feelin' fine!",
		"health":       "healthy",
		"health-level": 100,
		"code":         239, // a custom code, mostly to have it, and as a placeholder I guess, no biggie
	})
}

// isRequestAllowed(clientId, rulesId) -> {passes: boolean, remaining: number, resetTime: timestamp}
func (h *RatelimiterHandler) isRequestAllowed(c *gin.Context) {
	const useRistrettoCostCalc int64 = 0
	var messageFromClient IncomingMessage
	var userId string

	if err := c.ShouldBindJSON(&messageFromClient); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Malformed JSON, missing either 'client id' or 'rules id'"})
		return
	}

	algo := messageFromClient.Algorithm

	if !internal.VerifyAlgorithm(algo) {
		algo = h.Config.DefaultAlgorithm
	}

	userId = messageFromClient.ClientId
	requestUserHash := "ratelimit:" + userId + ":" + algo

	if entry, found := h.ristrettoCache.Get(requestUserHash); found {
		tooManyRequests := entry.HitCount >= h.Config.Limit
		if tooManyRequests {
			entry.HitCount++

			// now - "when first hit registered" + "configured period"
			resets := time.Duration(time.Now().UnixNano()) - time.Duration(entry.FirstHit) + h.Config.Period

			h.ristrettoCache.SetWithTTL(requestUserHash, entry, 0, resets)
			h.ristrettoCache.Wait()
			// needs headers
			c.Writer.Header().Set("Content-Type", "application/json")
			var hitcount int64 = 0

			if (h.Config.Limit - entry.HitCount) > 0 {
				hitcount = h.Config.Limit - entry.HitCount
			}

			c.Writer.Header().Set("RateLimit-Remaining", strconv.FormatInt(hitcount, 10))
			c.Writer.Header().Set("RateLimit-Reset", strconv.FormatInt(secondsUntilRatelimitReset(entry.FirstHit, h.Config.Period), 10))
			c.Writer.Header().Set("RateLimit-Limit", strconv.FormatInt(h.Config.Limit, 10))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"Passes":     false,
				"HitCount":   entry.HitCount,
				"FirstHit":   entry.FirstHit,
				"Remaining":  0,
				"ResetsUnix": resets.Seconds(),
			})
			h.Logger.Info("entity found in cache", zap.String("entity", entry.String()))
			return
		}
	}

	v, err := h.RedisConnection.ProcessRatelimitRequest(requestUserHash, int(h.Config.Period.Seconds()), int(h.Config.Limit), algo)

	// TODO: not sure about this branch, I mean, yeah it has to exist, but the internals could be a tad better
	if err != nil {
		c.Writer.Header().Set("Content-Type", "application/json")
		c.Writer.Header().Set("RateLimit-Remaining", strconv.FormatInt(v.Remaining, 10))
		c.Writer.Header().Set("RateLimit-Reset", strconv.FormatInt(secondsUntilRatelimitReset(v.FirstHit, h.Config.Period), 10))
		c.Writer.Header().Set("RateLimit-Limit", strconv.FormatInt(h.Config.Limit, 10))
		c.JSON(http.StatusInternalServerError, nil)

		o := internal.RedisEntry{
			HitCount: v.HitCount,
			FirstHit: v.FirstHit,
		}

		h.ristrettoCache.SetWithTTL(requestUserHash, o, 0, h.Config.Period-(time.Duration(time.Now().UnixNano())-time.Duration(o.FirstHit)))
		h.ristrettoCache.Wait()
		h.Logger.Info("error with Redis connection", zap.Error(err))
		return
	}

	newEntry := internal.RedisEntry{HitCount: v.HitCount, FirstHit: v.FirstHit}

	expiry := time.Unix(v.FirstHit, 0).Add(h.Config.Period)
	ttl := time.Until(expiry)

	if ttl > 0 {
		h.ristrettoCache.SetWithTTL(requestUserHash, newEntry, 0, ttl)
		h.ristrettoCache.Wait()
	}

	status := http.StatusOK
	if !v.Passes {
		status = http.StatusTooManyRequests
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.Header().Set("RateLimit-Remaining", strconv.FormatInt(v.Remaining, 10))
	c.Writer.Header().Set("RateLimit-Reset", strconv.FormatInt(secondsUntilRatelimitReset(v.FirstHit, h.Config.Period), 10))
	c.Writer.Header().Set("RateLimit-Limit", strconv.FormatInt(h.Config.Limit, 10))

	c.JSON(status, v)
}

type MessageToClient struct {
	Passes    bool
	ResetUnix int64
	ResetIso  string
	Limit     int64
	Remaining int64
}

func timeUntil(t int64) time.Time {
	return time.Unix(t+60, 0)
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
