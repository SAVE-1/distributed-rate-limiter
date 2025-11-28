# What is this project
This project is a distributed rate limiter, a service level -service, inspired by: 
- Design a Rate Limiter by Hello Interview (https://www.hellointerview.com/learn/system-design/problem-breakdowns/distributed-rate-limiter)
- Flexible rate limiting in a distributed environment by Wolt (https://www.youtube.com/watch?v=RHDPEA_DP44)

The project is currently work-in-progress

There is still plenty to do, such as:
- Better configurations, most of the variables are hard coded
- A small LUA optimization for atomicity and to avoid roundtrips to REDIS
    - For now, the extra roundtrip costs quite a lot of latency, at localhost the latency for now is around 4.5-5.2ms, but I expect it to get better with a REDIS-Lua optimization

# Requirements
- Go, version 1.24.5
- Docker desktop
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
