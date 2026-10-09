# Keystone — rate limiting

Fixed-window counters in Redis, atomic via a Lua script, applied per-IP
to the two mutating endpoints. Reads are not rate-limited; the cache
tier already shields the database.

## Defaults

| Scope | Default | Env override |
|-------|---------|--------------|
| `POST /shorten` | 60 req/min per IP | `KEYSTONE_RL_SHORTEN` |
| `POST /capabilities` | 30 req/min per IP | `KEYSTONE_RL_CAPABILITIES` |

Set either to `0` to disable that scope. If Redis itself is unreachable,
the limiter **fails open** (allows the request) and logs — a dead
limiter should never turn into a global 503. The bypass is counted as
`result=bypass_error` on the metric so it's observable.

## Behavior on block

- Status `429 Too Many Requests`
- Header `Retry-After: <seconds>`
- Body: `{"error":"rate limit exceeded","retry_after":<seconds>}`

## The Lua script — why it exists

Naive implementation would be:

```
INCR key
EXPIRE key 60
```

Between those two commands, a crash or a replica interleaving could
leave `key` incremented but with no TTL — permanent counter, limiter
stuck returning "blocked" forever. One Lua script runs the pair
atomically from Redis's perspective; nothing else interleaves.

This is the archetypal "I need multiple Redis ops to happen as one"
pattern. Lua scripts are the right answer.

## Testing it yourself

With the scaled stack running:

```
for /l %i in (1,1,70) do @curl -s -o nul -w "%%{http_code}\n" ^
  -X POST http://127.0.0.1:8090/shorten ^
  -H "Content-Type: application/json" ^
  -d "{\"url\":\"https://example.com\"}"
```

Expected: first ~60 return `201`, the rest return `429`.

Verify Retry-After:
```
curl -s -D - -X POST http://127.0.0.1:8090/shorten ^
  -H "Content-Type: application/json" ^
  -d "{\"url\":\"https://example.com\"}" ^
  -o nul | findstr /i "retry"
```

## Why this works across 3 replicas

Keystone's rate-limit state lives in **Redis**, not in the Go processes.
All replicas share the same counter per IP. If I'd used an in-process
map, three replicas would mean 3× the effective limit — a well-known
anti-pattern. See docs/scaling.md invariant #2.

## On the dashboard

Panel 10 — "Rate-limit decisions" — plots allowed vs blocked vs
bypass_error over time by scope. Blocked rising is either a legitimate
abuser or (more likely in dev) your own load test hitting the ceiling.
Bypass errors rising means Redis is misbehaving.
