-- KEYS[1] = hash key (e.g. ratelimit:abc-123)
-- ARGV[1] = period in seconds (e.g. 60)
-- ARGV[2] = limit (max hits per period)
-- Script returns: {passes, hitcount, firsthit, remaining, period}

redis.replicate_commands()
local key = KEYS[1]
local period = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local now = redis.call('TIME')
-- local now_in_millis = now[1]
local now_in_millis = tonumber(now[1])
local hitcount
local firsthit

if redis.call('EXISTS', key) == 1 then
    local data = redis.call('HMGET', key, 'hitcount', 'firsthit')
    hitcount = tonumber(data[1]) or 0
    firsthit = tonumber(data[2]) or now_in_millis    
    -- Check if window has expired
    if now_in_millis - firsthit >= period then
        -- Reset window
        hitcount = 0
        firsthit = now_in_millis
    end
else
    hitcount = 0
    firsthit = now_in_millis
end

-- Increment hit count for current request
hitcount = hitcount + 1

local passes = 1
local remaining = limit - hitcount
if hitcount > limit then
    passes = 0
    remaining = 0
end

redis.call('HMSET', key, 'hitcount', hitcount, 'firsthit', firsthit)

local nowSeconds = tonumber(now[1])
local remaining_ttl = firsthit + period - now_in_millis
redis.call('EXPIRE', key, math.max(remaining_ttl, 1))
    
return {passes, hitcount, firsthit, remaining, period}
