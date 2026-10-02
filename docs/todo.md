# TODO

## 2026-09-17
- [ ] Detect Redis disconnection/downtime - currently no reconnect or fallback logic if Redis goes down while the server is running

## 2026-09-21
- [ ] Make testcontainers instances independent of each other, it does make test suite slower to run on overall
    - This is still a maybe, well see if it is needed
- [ ] Pin down Redis version -- priority
- [ ] Set period duration to milliseconds (millis will support additional algorithms too, currently only fixed window is supported), currently minutes only are supported
- [ ] Make a test with two handlers, so they'll compete for for the same key -- priority
