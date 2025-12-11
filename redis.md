
some REDIS lua examples

```redis

-- SCRIPT LOAD ""
-- EVALSHA 1883f74f94f8751c4f23f11ce81c27b1834bcd18 1 ratelimit:abc 60 10
-- EVALSHA 1883f74f94f8751c4f23f11ce81c27b1834bcd18 ratelimit:abc 60 10
-- 1883f74f94f8751c4f23f11ce81c27b1834bcd18

-- EVALSHA e4987a0767079f5fe15d3889771cf808b1c8d1b0 1 ratelimit:abc 60 10

-- the script load does not like comments for some reason I guess, 
SCRIPT LOAD "
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
    
    return {passes, hitcount, firsthit, remaining}"
> f41e7c759db7be1547b22388ad75614c130345a9

EVALSHA f41e7c759db7be1547b22388ad75614c130345a9 1 ratelimiter:abc 60 10

1) "1"
2) "1"
3) "1765361934"
4) "9"

```
