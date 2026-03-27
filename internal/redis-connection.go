package internal

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type RedisEntry struct {
	HitCount int64 `redis:"hitcount"`
	FirstHit int64 `redis:"firsthit"`
	ok       bool
}

func (i RedisEntry) Found() bool {
	return i.ok
}

func (i *RedisEntry) Reset() {
	i.HitCount = 1
	i.FirstHit = time.Now().Unix()
}

// Script returns: {passes, hitcount, firsthit, remaining, period}
type RedisResponse struct {
	Passes     bool
	HitCount   int64
	FirstHit   int64
	Remaining  int64
	ResetsUnix int64
}

func (i RedisResponse) String() string {
	var b strings.Builder
	b.Grow(128) // preallocate enough
	b.WriteString("[ passes: ")
	b.WriteString(strconv.FormatBool(i.Passes))
	b.WriteString(", hit-count: ")
	b.WriteString(strconv.FormatInt(i.HitCount, 10))
	b.WriteString(", first-hit: ")
	b.WriteString(strconv.FormatInt(i.FirstHit, 10))
	b.WriteString(", remaining: ")
	b.WriteString(strconv.FormatInt(i.Remaining, 10))
	b.WriteString(", resets-unix: ")
	b.WriteString(strconv.FormatInt(i.ResetsUnix, 10))
	b.WriteString(" ]")

	return b.String()
}

type RedisConnection struct {
	Address     string
	Username    string
	Password    string
	Logger      *zap.Logger
	RedisClient *redis.Client
}

func NewRedisConnection(address, username, password string, logger *zap.Logger) *RedisConnection {
	return &RedisConnection{
		Address:     address,
		Username:    username,
		Password:    password,
		Logger:      logger,
		RedisClient: nil,
	}
}

func (i RedisEntry) String() string {
	var b strings.Builder
	b.Grow(128) // preallocate enough

	b.WriteString("[ hit-count: ")
	b.WriteString(strconv.FormatInt(i.HitCount, 10))
	b.WriteString(", first-hit: ")
	b.WriteString(strconv.FormatInt(i.FirstHit, 10))
	b.WriteString(" ]")

	return b.String()
}

func (h *RedisConnection) ProcessSomething(hashName string, period int, limit int) (RedisResponse, error) {
	if h.RedisClient == nil {
        h.Logger.Error("ERROR: RedisClient is NIL!")
        return RedisResponse{}, errors.New("redis client not initialized")
    }
	ctx := context.Background()
		
	values := []interface{}{period, limit}

	var e RedisResponse

	t, err := tokenBucket.Run(ctx, h.RedisClient, []string{hashName}, values...).Result()
	
	if err != nil {
		h.Logger.Error("error with redis")
		return RedisResponse{}, err
	}

	values = t.([]interface{})

	l := values[0].(int64)

	var passes bool
	if l == 1 {
		passes = true
	} else {
		passes = false
	}

	hitCount := values[1].(int64)

	firstHit := values[2].(int64)

	remaining := values[3].(int64)

	e.Passes = passes
	e.HitCount = hitCount
	e.FirstHit = firstHit
	e.Remaining = remaining

	return e, nil
}

// https://redis.uptrace.dev/guide/lua-scripting.html#redis-script
// https://github.com/go-redis/redis_rate/blob/v9/rate.go
// https://github.com/go-redis/redis_rate/blob/v9/lua.go

var tokenBucket = redis.NewScript(`
-- KEYS[1] = hash key (e.g. ratelimit:abc-123)
-- ARGV[1] = period in seconds (e.g. 60)
-- ARGV[2] = limit (max hits per period)
-- Script returns: {passes, hitcount, firsthit, remaining, period}

redis.replicate_commands()
local key = KEYS[1]
local period = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local now = redis.call('TIME')
-- local now_in_millis = now[1]
local now_in_millis = tonumber(now[1])
local hitcount
local firsthit

if redis.call('EXISTS', key) == 1 then
    local data = redis.call('HMGET', key, 'hitcount', 'firsthit')
    hitcount = tonumber(data[1]) or 0
    firsthit = tonumber(data[2]) or now_in_millis    
    -- Check if window has expired
    if now_in_millis - firsthit >= period then
        -- Reset window
        hitcount = 0
        firsthit = now_in_millis
    end
else
    hitcount = 0
    firsthit = now_in_millis
end

-- Increment hit count for current request
hitcount = hitcount + 1

local passes = 1
local remaining = limit - hitcount
if hitcount > limit then
    passes = 0
    remaining = 0
end

redis.call('HMSET', key, 'hitcount', hitcount, 'firsthit', firsthit)

local nowSeconds = tonumber(now[1])
local remaining_ttl = firsthit + period - now_in_millis
redis.call('EXPIRE', key, math.max(remaining_ttl, 1))
    
return {passes, hitcount, firsthit, remaining, period}
`)

// returns the byte count, or the cost, of internal.RedisEntry -struct
func (h *RedisConnection) GetRedisEntryCostFunction() func(value RedisEntry) int64 {
	return func(value RedisEntry) int64 {
		/*
			a more futureproof version would be
			return int64(unsafe.Sizeof(value))

			j := internal.RedisEntry{
				HitCount: 1,
				FirstHit: 1,
			}

			fmt.Println(int64(unsafe.Sizeof(j))) // == 24

			so along with the padding, it should be:
			8 + 8 + 1 + 7 = 24

			return 24 is a hotpath optimization, because the size of the struct is well known.
		*/
		return 24
	}
}

func (h *RedisConnection) HIncrBy(ctx context.Context, key string, field string, count int64) error {
	err := h.RedisClient.HIncrBy(ctx, key, field, count).Err()

	if err != nil {
		h.Logger.Error("error with incrementing")
		return err
	}

	return nil
}

// OpenRedisConnection initializes the Redis client, and attempts tp validate client connection by issuing a PING.
// Returns a ready-to-use client, or an error if the server is unreachable,
// the address is misconfigured, or authentication fails.
// The caller is responsible for closing the client when done.
func OpenRedisConnection(r *RedisConnection) (*redis.Client, error) {
	// these will be moved into environment vars in the future
	redisClient := redis.NewClient(&redis.Options{
		Addr:     r.Address,
		Username: r.Username, // use your Redis user. More info https://redis.io/docs/latest/operate/oss_and_stack/management/security/acl/
		Password: r.Password, // use your Redis password
	})
	ctx := context.Background()

	_, err := redisClient.Ping(ctx).Result() // *net.OpError
	
	if err != nil {
		return nil, err
	}

	r.Logger.Info("Redis connection initialized successfully ")

	return redisClient, nil
}

func (h *RedisConnection) CloseRedisConnection() error {
	return h.RedisClient.Close()
}
