package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/SAVE-1/distributed-rate-limiter/ratelimiter"
	"github.com/urfave/cli/v3"
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

func main() {

	cmd := &cli.Command{
		UseShortOptionHandling: true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "redisaddr",
				Value: "",
				Usage: "redis ip address",
			},
			&cli.StringFlag{
				Name:  "redisuser",
				Value: "",
				Usage: "redis user name",
			},
			&cli.StringFlag{
				Name:  "redispassword",
				Value: "",
				Usage: "redis user password",
			},
			&cli.IntFlag{
				Name:  "period",
				Value: 60,
				Usage: "user request reset period",
			},
			&cli.IntFlag{
				Name:  "reqlimit",
				Value: 2,
				Usage: "user request limit"},
			&cli.BoolFlag{
				Name:  "redis",
				Value: false,
				Usage: "is no redis allowed",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			seconds, err := time.ParseDuration(strconv.Itoa(int(cmd.Int("period"))) + "s")
			if err != nil {
				return err
			}

			config := ratelimiter.RateLimiterConfiguration{
				RedisAddress:             cmd.String("redisaddr"),
				RedisUsername:            cmd.String("redisuser"),
				RedisPassword:            cmd.String("redispassword"),
				Period:                   seconds,
				Limit:                    int64(cmd.Int("reqlimit")),
				AllowStartupWithoutRedis: cmd.Bool("redis"),
			}

			if err := ratelimiter.Start(config); err != nil {
				return err
			}
			return nil
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}

}
