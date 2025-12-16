# What is this project
This project is a distributed rate limiter, a service level -service, inspired by: 
- Design a Rate Limiter by Hello Interview (https://www.hellointerview.com/learn/system-design/problem-breakdowns/distributed-rate-limiter)
- Flexible rate limiting in a distributed environment by Wolt (https://www.youtube.com/watch?v=RHDPEA_DP44)

The project is currently work-in-progress

There is still plenty to do, such as:
- Better configurations, most of the variables are hard coded

# Requirements
- Go, at least version 1.24.5
- Docker desktop
- k6, if you want to run loadtests

# Performance
## Load Testing, best case
The rate limiter was load tested using k6 with a constant arrival rate of 12,000 requests per second sustained for 60 seconds on a single node.

The test was executed in WSL, with Redis and the rate-limiter service running on the same network, on an Intel i5-12600K.

Test characteristics:
- Traffic pattern: constant arrival rate
- Client cardinality: high (unique client per request)
- Endpoint: POST /v1/ratelimit

Results:
- Sustained throughput: ~12k requests/sec
- avg latency: ~0.75 ms
- med latency: ~0.43 ms
- p95 latency: ~1.2 ms
- p99 latency: ~8 ms
- Correct rate-limit enforcement (HTTP 429 under excess load)

## Nice to have
- Task, https://taskfile.dev/

# Setting up local REDIS with management console for the IP/UserId/API-key cache
```powershell
    docker run -d --name rate-limiter-redis-stack -p 6379:6379 -p 8001:8001 -e REDIS_ARGS="--requirepass mypassword" redis/redis-stack:latest
```

# How to run
Either with
```powershell
    task run
```
or
```powershell
    go build -o bin/ratelimiter.exe
    ./bin/ratelimiter.exe
```

## Management console URL
```
    http://localhost:8001
```

## why REDIS stored procedures are used?
- to get a single source of truth for the monotonic clock (the redis server)
- to reduce round trips by one
- to get atomicity

## The API

### What the rate limiter receives from client

```
{
    "ClientId": either API-key, user id or ip, it doesn't actually matter right now as the string is used as-is, with no additional processing,
    "RulesId": to be implemented
}
```

### What the rate limiter returns to client
```
{
	"passes":     bool, did the request pass,
	"reset_unix": 64-bit integer, when will the users request limit reset,
	"reset_iso":  string, reset_unix as a string in RFC3339-format,
	"limit":      64-bit integer, the servers request limit,
	"remaining":  64-bit integer, how many requests are remaining
}
```
