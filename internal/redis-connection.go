package internal

import (
	"context"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

var (
	redisClient *redis.Client
)

type RedisEntry struct {
	HitCount int64  `redis:"hitcount"`
	FirstHit int64  `redis:"firsthit"`
	// Window   string `redis:"window"`
	ok       bool
}

func (i RedisEntry) Ok() bool {
	return i.ok
}

func (i RedisEntry) String() string {
	return fmt.Sprintf("[ hit-count: %d, first-hit: %d ]", i.HitCount, i.FirstHit)
}

func InitRedis(address string, username string, password string) error {
	// these will be moved into environment vars in the future
	redisClient = redis.NewClient(&redis.Options{
		Addr:     address,
		Username: username,    // use your Redis user. More info https://redis.io/docs/latest/operate/oss_and_stack/management/security/acl/
		Password: password, // use your Redis password
	})
	ctx := context.Background()

	_, err := redisClient.Ping(ctx).Result()

	if err != nil {
		return err
	}

	return nil
}

func CloseRedis() error {
	return redisClient.Close()
}

func AddHashToRedis(hash string, fields RedisEntry) (int64, error) {
    ctx := context.Background()
	
    return redisClient.HSet(ctx, hash, []string{
        "hitcount", strconv.FormatInt(fields.HitCount, 10),
        "firsthit", strconv.FormatInt(fields.FirstHit, 10),
    }).Result()
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
func GetHashFromRedis(hash string) (RedisEntry, error) {
	ctx := context.Background()
	val, hgetErr := redisClient.HGetAll(ctx, hash).Result()
	fmt.Println("hgetErr:", hgetErr)
	fmt.Println("vals:", val)
	if hgetErr != nil {
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
