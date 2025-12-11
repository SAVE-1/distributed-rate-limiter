#!lua name=mylib

local function rate_limiter_token_bucket(hashName, period, limit)
    -- KEYS[1] = hash key (e.g. ratelimit:abc-123)
    -- ARGV[1] = period in seconds (e.g. 60)
    -- ARGV[2] = limit (max hits per period)
    -- Script returns: {passes, hitcount, firsthit, remaining}

    local key = KEYS[1]
    local period = tonumber(ARGV[1])
    local limit = tonumber(ARGV[2])

    local now = redis.call('TIME')
    local nowSeconds = tonumber(now[1])

    local hitcount
    local firsthit

    if redis.call('EXISTS', key) == 1 then
        local data = redis.call('HMGET', key, 'hitcount', 'firsthit')
        hitcount = tonumber(data[1]) or 0
        firsthit = tonumber(data[2]) or nowSeconds

        if nowSeconds - firsthit >= period then
            hitcount = 1
            firsthit = nowSeconds
        else
            hitcount = hitcount + 1
        end
    else
        hitcount = 1
        firsthit = nowSeconds
    end

    local passes = 1
    local remaining = limit - hitcount

    if hitcount > limit then
        passes = 0
        remaining = 0
    end

    redis.call('HMSET', key, 'hitcount', hitcount, 'firsthit', firsthit)

   local remaining_ttl = firsthit + period - nowSeconds

     if remaining_ttl > 0 then
         redis.call('EXPIRE', key, remaining_ttl)
     else
        redis.call('EXPIRE', key, period)
    end
    
    return {passes, hitcount, firsthit, remaining}

end

redis.register_function('rate_limiter_token_bucket', rate_limiter_token_bucket)
