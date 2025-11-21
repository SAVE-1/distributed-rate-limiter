# What is this project
This project is a distributed rate limiter, a service level -service, inspired by: 
- Design a Rate Limiter by Hello Interview (https://www.hellointerview.com/learn/system-design/problem-breakdowns/distributed-rate-limiter)
- Flexible rate limiting in a distributed environment by Wolt (https://www.youtube.com/watch?v=RHDPEA_DP44)

The project is currently work-in-progress

There is still plenty to do, such as:
- Better configurations, most of the variables are hard coded
- A small LUA optimization for atomicity and to avoid roundtrips to REDIS

# Requirements
- Go, version 1.24.5
- Docker desktop

# Setting up local REDIS with management console for the IP/UserId/API-key cache
```powershell
    docker run -d --name rate-limiter-redis-stack -p 6379:6379 -p 8001:8001 -e REDIS_ARGS="--requirepass mypassword" redis/redis-stack:latest
```

## Management console URL
```
    http://localhost:8001
```
