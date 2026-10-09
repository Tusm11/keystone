# Keystone — horizontal scaling

The origin is stateless by design: all shared state lives in Postgres,
Redis, and Redis Streams. That means it can be scaled horizontally — N
copies behind a load balancer, with no sticky-session or shared-memory
coordination needed.

## Topology (scale profile)

```
                  ┌────────────────┐
                  │   edge Worker  │   points at :8090 instead of :8080
                  └────────────────┘
                           │
                           ▼
                   ┌───────────────┐
                   │  nginx (lb)   │   least_conn
                   │     :8090     │   Docker DNS re-resolve every 5s
                   └───────────────┘
                     │     │     │
                     ▼     ▼     ▼
                  origin1 origin2 origin3    N replicas via --scale
                     │     │     │
             ┌───────┴─────┴─────┴───────┐
             ▼                           ▼
          Postgres                     Redis
          (shared state)         (cache + stream)
```

## Running it

```
docker compose --profile scale up -d --build --scale origin=3
```

That builds the origin image, starts 3 replicas, 1 clickworker, and the
nginx LB on :8090. Postgres and Redis are shared.

Point the edge at the LB for the scaled read path:
```
cd edge
set ORIGIN_URL=http://host.docker.internal:8090
npm run dev
```

Or hit the LB directly:
```
curl -i http://127.0.0.1:8090/health
```

Each response carries an `X-Served-By: <container-hostname>` header so you
can see which replica served it. Loop a few times to prove round-robin:

```
for /l %i in (1,1,10) do @curl -s -D - -o nul http://127.0.0.1:8090/health | findstr X-Served-By
```

Expected: three distinct hostnames (one per replica) spraying across the
10 calls.

## Why it works — the stateless invariants

For an origin to be scaled behind a stateless LB, four invariants must hold:

1. **No in-process cache that must be coherent.** Keystone's hot cache is
   Redis; all replicas see the same cache. If we had added a per-pod L1
   cache, we'd need pub/sub invalidation.
2. **No in-process rate-limit state.** v1 doesn't rate-limit yet; when we
   add it, counters must live in Redis, not in Go maps, or N replicas =
   N× the limit.
3. **No in-process session state.** We have no sessions.
4. **Idempotent writes.** `POST /shorten` with the same input twice ends
   up creating two different short codes — the client gets back whichever
   won. For true idempotency we'd need an Idempotency-Key header; v1
   doesn't need it.

The click pipeline fans across replicas trivially: every replica writes
to the same Redis Stream; the clickworker consumer group guarantees each
message is processed exactly once across all consumers.

## How to see the LB working

### 1. Confirm all three replicas registered

```
curl -s http://127.0.0.1:8090/health
curl -s http://127.0.0.1:8090/health
curl -s http://127.0.0.1:8090/health
```

The `host` field in the JSON rotates between three names — that's
nginx's least_conn picking different backends.

### 2. Watch logs stream from all three replicas live

```
docker compose --profile scale logs -f origin
```

Each log line is prefixed with the container short name; you'll see
different ones serving requests.

### 3. Kill a replica mid-traffic and watch failover

```
docker compose --profile scale ps
docker kill keystone-origin-2
```

Keep hammering the LB with curl. Nginx's `max_fails=3 fail_timeout=10s`
takes the dead one out of rotation within a second. Response rate is
unaffected.

Bring it back:
```
docker compose --profile scale up -d --scale origin=3
```

### 4. Scale up or down without downtime

```
docker compose --profile scale up -d --scale origin=5    # more replicas
docker compose --profile scale up -d --scale origin=1    # scale back down
```

Docker's internal DNS gets the new A records; nginx re-resolves every 5s
(from the `resolver 127.0.0.11 valid=5s` directive) and new replicas
join rotation.

## Load testing against the LB

Point k6 at :8090 instead of :8080:
```
docker compose --profile loadtest run --rm \
  -e BASE_URL=http://host.docker.internal:8090 \
  k6 run scripts/read-warm.js
```

Expect **similar p99 to origin-direct, HIGHER throughput** — 3 replicas
can serve ~3x the concurrent in-flight requests before any one of them
saturates. If your laptop has enough cores, you'll see req/s climb.

## What this proves for interviews

A URL shortener behind a load balancer is unremarkable. **A URL shortener
whose author can articulate WHY it's safe to run N copies** — that's the
signal. The four stateless invariants above are the right thing to recite
when someone asks "how would you scale this?"
