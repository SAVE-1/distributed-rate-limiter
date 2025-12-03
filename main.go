package main

import (
	"fmt"
	"time"

	"github.com/SAVE-1/distributed-rate-limiter/ratelimiter"
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
	config := ratelimiter.RateLimiterConfiguration{
		RedisAddress:             "127.0.0.1:6379",
		RedisUsername:            "default",
		RedisPassword:            "mypassword",
		Period:                   1 * time.Minute,
		Limit:                    2,
		AllowStartupWithoutRedis: false,
	}

	if err := ratelimiter.Start(config); err != nil {
		fmt.Println(err)
	}
}
