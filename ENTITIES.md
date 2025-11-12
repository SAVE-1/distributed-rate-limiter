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

