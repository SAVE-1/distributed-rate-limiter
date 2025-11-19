# Entities

# REDIS
```redis

    key -- IP, API-key or used id

    initial schema:
    - key:   raterlimit:<user identifier>:<identifier>
    - value: hitcount int
    - value: firsthit int
    - value: window   int

    same in go:

    // UserIdentificationType
    const (
        IP = iota
        UserId 
        ApiKey
    )

    type RatelimiterKey struct {
        UserIdentifier          string
        UserIdentificationType  int
        HitCount                int
        FirstHit                int64
        Window                  int64
    }

    examples in REDIS:
    HSET ratelimit:ip:127.0.0.1 hitcount 0 firsthit 1762780059 window 1762772919
    HSET ratelimit:apikey:111-111-111
    HSET ratelimit:userid:18

```

# Response from rate limiter

from: https://www.ietf.org/archive/id/draft-polli-ratelimit-headers-02.html#section-1.3
    This proposal defines syntax and semantics for the following header fields:
    - RateLimit-Limit: containing the requests quota in the time window;
    - RateLimit-Remaining: containing the remaining requests quota in the current window; 
    - RateLimit-Reset: containing the time remaining in the current window, specified in seconds.

An example:
```

when too many requests
HTTP/2 429 Too Many Requests
Content-Type: application/json
RateLimit-Remaining: 0
RateLimit-Reset: 
RateLimit-Limit: 20

{
  "title": "Rate limit exceeded",
  "detail": "Too many requests in a given amount of time, please try again later."
}

```

