# What is this project
A Go-based distributed rate limiter implemented as a microservice with Redis as a shared state backend, focusing on testable design, scalability and performance.

The project is intentionally scoped as a learning exercise rather than a production-ready solution.

# Motivation
Rate limiting is a common backend concern used to protect services, enforce fair usage, and improve overall system reliability.
This project was built to better understand:
- How rate limiting can be implemented as a standalone backend service
- Trade-offs involved in using shared state (Redis) for coordination
- Concurrency behavior and performance under load
- How system design decisions impact latency and throughput

The implementation was inspired by backend engineering talks and real-world use cases discussed in industry presentations.

# High-Level Design
- Language: Go
- Architecture: Standalone microservice
- State management: Redis (shared backend)
- API: HTTP-based interface for rate-limited requests
- Focus areas: correctness, testability, and performance experimentation

Redis is used to coordinate rate-limit state across requests, keeping the service stateless from the application perspective.

# Current Scope
## Implemented
- Core rate-limiting logic
- Redis integration
	- Redis is used to enable:
		- Shared state across rate limiter instances
		- A single source of truth for time and counters
		- Atomic updates via Lua scripts
		- Reduced network round-trips for critical paths
	    - Versioned rate-limiting algorithms, with identifiers stored in Redis to allow multiple strategies and versions to coexist
- Basic service setup and configuration
- Load testing using k6
- Local development setup

## Out of Scope / Not Implemented
- Multi-node deployment
- Advanced fault tolerance
- Dynamic configuration management
- Production-grade observability
- Security hardening

These are intentionally left out to keep the project focused and manageable.

# Running the Project
## Prerequisites
- Go, at least version 1.24.5
- Redis
- Docker (optional)
- k6 (for load testing)

## Nice to have
- Task, https://taskfile.dev/
- HTTPYac extension in vscode

# Local Run (example)
- Start Redis locally
- Run the service
	- If Task is installed, the server can be run with "task run" in cli
- Send HTTP requests to the rate-limited endpoint
	- IF HTTPYac is installed, there is a .http file containing messages in the project root

# Performance
## Load Testing, best case
The rate limiter was load tested using k6 with a constant arrival rate of 12,000 requests per second sustained for 60 seconds on a single node.

The test was executed in WSL, with Redis and the rate-limiter service running on the same network, on an Intel i5-12600K.

Test characteristics:
- Traffic pattern: constant arrival rate
- Client cardinality: high (unique client per request)
- Endpoint: POST /v1/ratelimit

Results, every user is unique:
- Sustained throughput: ~12k requests/sec
- avg latency: ~1.68 ms
- med latency: ~0.58 ms
- p95 latency: ~5.39 ms
- p99 latency: ~19 ms

Results, 100 unique users:
- Sustained throughput: ~12k requests/sec
- avg latency: ~1.22 ms
- med latency: ~0.23 ms
- p95 latency: ~1.07 ms
- p99 latency: ~35 ms

These benchmarks are intended to explore latency behavior and throughput limits under controlled conditions, rather than to claim production-level performance. 
Running Redis and the service on a single node minimizes network overhead and highlights the cost of synchronization and Lua execution.
The results helped identify concurrency limits and informed how the design would need to evolve for multi-node deployments (e.g. sharding, local caching, or alternative coordination strategies).

# Design Decisions & Trade-offs
- Redis was chosen for simplicity and atomic operations, at the cost of added latency and a shared dependency.
- Lua scripts reduce round-trips but increase coupling to Redis.
- The service is stateless by design, simplifying horizontal scaling but shifting consistency concerns to Redis.

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

# Disclaimer
This project is intended for learning and experimentation and does not aim to meet production requirements.

## Setting up local REDIS with management console for the IP/UserId/API-key cache
```powershell
    docker run -d --name rate-limiter-redis -p 6379:6379 -p 8001:8001 -e REDIS_ARGS="--requirepass mypassword" redis/redis-stack:latest
```

## Management console URL
```
    http://localhost:8001
```

# The API
### What the rate limiter receives from client
```
{
	"ClientId": Identifier used for rate limiting (e.g. API key, user ID, or IP address). The value is currently treated as an opaque string.
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

