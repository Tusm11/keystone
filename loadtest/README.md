# Keystone load tests

Four k6 scenarios, each isolating one property of the architecture.
All scripts are self-contained: `setup()` mints its own codes and passes
them to VUs via setup's return value — the only reliable way to share
data across k6 contexts.

## Important — what you're measuring

The default `BASE_URL` is the **edge Worker running under `wrangler dev`**
(Node + Miniflare). That's a dev harness, not a production runtime, and
Docker Desktop's `host.docker.internal` adds another 10–50ms per request
on Mac/Windows. Expect local p99 of a few hundred ms to >1s.

**That is not what the real system does.** Cloudflare's V8 isolates at a
PoP serve the same code in 5–30ms. The right local benchmark for the Go
stack is to bypass the edge dev server and hit origin directly:

```
docker compose --profile loadtest run --rm \
  -e BASE_URL=http://host.docker.internal:8080 \
  k6 run scripts/read-warm.js
```

Origin-direct p99 locally should be < 100ms. That number is a decent
proxy for how the Go stack will behave in production.

## Prerequisites

- `docker compose up -d` (Postgres + Redis)
- `cd origin && go run ./cmd/server` (origin, with env vars set)
- `cd origin && go run ./cmd/clickworker` (worker)
- `cd edge && npm run dev` (edge Worker on :8787) — only needed when
  BASE_URL points at the edge

## Scenarios

### read-warm — hot-path baseline
Mints N codes, warms every one, then 100 VUs hit random codes for 30s.
```
docker compose --profile loadtest run --rm k6 run scripts/read-warm.js
```

### read-cold — worst-case miss path
Each VU targets a disjoint slice of fresh codes. Every request misses
all caches → Postgres.
```
docker compose --profile loadtest run --rm k6 run scripts/read-cold.js
```

### stampede — prove the singleflight defense
200 VUs hit one fresh code simultaneously.
```
docker compose --profile loadtest run --rm k6 run scripts/stampede.js
```

Direct measurement of what the defense bought you:
```
:: before run
docker exec -it keystone-postgres psql -U keystone -d keystone -c "SELECT xact_commit FROM pg_stat_database WHERE datname='keystone';"
:: run the stampede, then re-run that query. Delta ≈ 1 means singleflight worked.
```

### mixed — 95% read / 5% write at 300 req/s for 45s
```
docker compose --profile loadtest run --rm k6 run scripts/mixed.js
```

## Scaling

Default N is 300–500 codes. Override:
```
docker compose --profile loadtest run --rm k6 run -e N=1000 scripts/read-warm.js
```

Setup is sequential, so N=500 adds ~50–100s of setup time; the
measurement phase is unchanged.

## What each threshold means

k6 thresholds are **assertions**, not goals. A ✗ means the system
didn't meet the committed bar. The right move is: investigate (slow
dependency? resource exhaustion?), not loosen the threshold. The
thresholds in this repo are deliberately loose because the environment
is `wrangler dev` + Docker Desktop + a laptop — not production.

For a portfolio screenshot, run against origin directly (`BASE_URL=
http://host.docker.internal:8080`) and capture those numbers. They
reflect the actual Go backend, which is what you're defending in an
interview — not the edge dev harness.
