package internal

import (
	"context"
	"fmt"
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

// {passes, hitcount, firsthit, remaining}
type RedisResponse struct {
	Passes     bool
	HitCount   int64
	FirstHit   int64
	Remaining  int64
	ResetsUnix int64
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
	ctx := context.Background()
	values := []interface{}{period, limit}

	var e RedisResponse

	t, err := tokenBucket.Run(ctx, h.RedisClient, []string{"ratelimiter:abc"}, values...).Result()

	if err != nil {
		return RedisResponse{}, err
	}

	values = t.([]interface{})

	// {passes, hitcount, firsthit, remaining}
	fmt.Println(values)

	passes, ok := values[0].(bool)
	if !ok {
		fmt.Println("2.")
	}

	hitCount, ok := values[1].(int64)
	if !ok {
		fmt.Println("3.")
	}

	firstHit, ok := values[2].(int64)
	if !ok {
		fmt.Println("4.")
	}

	remaining, ok := values[3].(int64)
	if !ok {
		fmt.Println("5.")
	}

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
-- Script returns: {passes, hitcount, firsthit, remaining}


redis.replicate_commands()

local key = KEYS[1]
local period = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local now = redis.call('TIME')
local nowSeconds = tonumber(now[1])

local hitcount
local firsthit

if redis.call('EXISTS', key) == 1 then
    local data = redis.call('HMGET', key, 'hitcount', 'firsthit')
    hitcount = tonumber(data[1]) or 0
    firsthit = tonumber(data[2]) or nowSeconds

    if nowSeconds - firsthit >= period then
        hitcount = 1
        firsthit = nowSeconds
    else
        hitcount = hitcount + 1
    end
else
    hitcount = 1
    firsthit = nowSeconds
end

local passes = 1
local remaining = limit - hitcount

if hitcount > limit then
    passes = 0
    remaining = 0
end

redis.call('HMSET', key, 'hitcount', hitcount, 'firsthit', firsthit)

local remaining_ttl = firsthit + period - nowSeconds

if remaining_ttl > 0 then
     redis.call('EXPIRE', key, remaining_ttl)
else
    redis.call('EXPIRE', key, period)
end
    
return {passes, hitcount, firsthit, remaining}
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

// opens a redis connection
func OpenRedisConnection(r *RedisConnection) (*redis.Client, error) {
	// these will be moved into environment vars in the future
	redisClient := redis.NewClient(&redis.Options{
		Addr:     r.Address,
		Username: r.Username, // use your Redis user. More info https://redis.io/docs/latest/operate/oss_and_stack/management/security/acl/
		Password: r.Password, // use your Redis password
	})
	ctx := context.Background()

	_, err := redisClient.Ping(ctx).Result()

	if err != nil {
		r.Logger.Error("REDIS initialization error", zap.Error(err))
		return nil, err
	}

	r.Logger.Info("No error with REDIS initialization")

	return redisClient, nil
}

func (h *RedisConnection) CloseRedis() error {
	return h.RedisClient.Close()
}

// adds a hash with the name of hash to the redis instance
func (h *RedisConnection) AddHashToRedis(hash string, fields RedisEntry, expiration time.Duration) error {
	ctx := context.Background()

	pipe := h.RedisClient.Pipeline()
	pipe.HSet(ctx, hash, "hitcount", strconv.FormatInt(fields.HitCount, 10))
	pipe.HSet(ctx, hash, "firsthit", strconv.FormatInt(fields.FirstHit, 10))
	pipe.Expire(ctx, hash, expiration)
	_, err := pipe.Exec(ctx)
	return err
}

// GetHashFromRedis retrieves a Redis hash and parses it into a RedisEntry.
//
// If the hash exists, the returned RedisEntry contains the parsed fields
// "HitCount" and "FirstHit" as int64 values and "Window" as a string.
// If parsing of "HitCount" or "FirstHit" fails, the corresponding field
// is set to -1.
//
// If the hash does not exist, GetHashFromRedis returns an RedisEntry with empty set to true
// and a nil error.
//
// If there is an error fetching the hash from Redis, the error is returned
// and the RedisEntry is empty.
func (h *RedisConnection) GetHashFromRedis(hash string) (RedisEntry, error) {
	ctx := context.Background()
	val, hgetErr := h.RedisClient.HGetAll(ctx, hash).Result()
	if hgetErr != nil {
		h.Logger.Error("REDIS library error with HGetAll", zap.Error(hgetErr))
		return RedisEntry{}, hgetErr
	}

	if len(val) == 0 {
		return RedisEntry{ok: false}, nil
	}

	hitcount, hitCountErr := strconv.ParseInt(val["hitcount"], 10, 64) // Parse as base 10 and store as int64
	if hitCountErr != nil {
		hitcount = -1
	}

	firsthit, firstHitErr := strconv.ParseInt(val["firsthit"], 10, 64) // Parse as base 10 and store as int64
	if firstHitErr != nil {
		firsthit = -1
	}

	return RedisEntry{
		HitCount: hitcount,
		FirstHit: firsthit,
		ok:       true,
	}, nil
}
