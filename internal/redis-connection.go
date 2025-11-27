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

func (i RedisEntry) Ok() bool {
	return i.ok
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

	return redisClient, nil
}

func (h *RedisConnection) CloseRedis() error {
	return h.RedisClient.Close()
}

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
	fmt.Println("hgetErr:", hgetErr)
	fmt.Println("vals:", val)
	if hgetErr != nil {
		h.Logger.Error("REDIS error", zap.Error(hgetErr))
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
