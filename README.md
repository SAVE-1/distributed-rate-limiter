# What is this project
This project is a distributed rate limiter, service level -service, inspired by: Design a Rate Limiter by Hello Interview (https://www.hellointerview.com/learn/system-design/problem-breakdowns/distributed-rate-limiter) and Flexible rate limiting in a distributed environment by Wolt (https://www.youtube.com/watch?v=RHDPEA_DP44)

# Setting up local REDIS with management console for the IP/UserId/API-key cache
```powershell
    docker run -d --name rate-limiter-redis-stack -p 6379:6379 -p 8001:8001 -e REDIS_ARGS="--requirepass mypassword" redis/redis-stack:latest
```

## Management console URL
```
    http://localhost:8001
```
