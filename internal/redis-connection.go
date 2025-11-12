package internal

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

var (
	redisClient *redis.Client
)

/*

	"HitCount", "1",
	"FirstHit", time.Now().String(),
	"Window", "1min",

*/

type RedisHit struct {
	HitCount int
	FirstHit int
	Window   string
}

func InitRedis() {
	redisClient = redis.NewClient(&redis.Options{
		Addr:     "127.0.0.1:6379",
		Username: "default",    // use your Redis user. More info https://redis.io/docs/latest/operate/oss_and_stack/management/security/acl/
		Password: "mypassword", // use your Redis password
	})

	ctx := context.Background()

	hashFields := []string{
		"model", "Deimos",
	}

	res1, err := redisClient.HSet(ctx, "bike:1", hashFields).Result()
	if err != nil {
		panic(err)
	}

	fmt.Println(res1) // >>> 4

	res3, err := redisClient.HGet(ctx, "bike:1", "price").Result()

	if err != nil {
		panic(err)
	}

	fmt.Println(res3) // >>> 4972
}

func AddHash(hash string, fields []string) (int64, error) {
	ctx := context.Background()
	res1, err := redisClient.HSet(ctx, "bike:1", fields).Result()
	if err != nil {
		return 0, err
	}

	return res1, nil
}

func GetHash(hash string) int {
	return 0
}
