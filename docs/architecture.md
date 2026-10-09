# Keystone — architecture (v0.1)

Short-link infrastructure for the agent era. This doc captures what the
code currently does, so a new reader (human or agent) can orient without
reading every file.

## Topology

```
   Client / Agent
        │
        ▼
  ┌──────────────┐
  │  Edge Worker │   TypeScript · Hono · Cloudflare Workers runtime
  │   (edge/)    │   localhost:8787 in dev
  └──────────────┘
        │ HTTP (JSON)
        ▼
  ┌──────────────┐
  │  Go origin   │   Go · chi · pgx · redis/go-redis
  │  (origin/)   │   localhost:8080 in dev
  └──────────────┘
        │
   ┌────┴────┐
   ▼         ▼
┌───────┐ ┌──────────┐
│ Redis │ │ Postgres │
│ cache │ │  (truth) │
└───────┘ └──────────┘
```

## Request paths

### POST /shorten  (write path)

Edge receives JSON `{url}`, forwards to origin. Origin generates a random
7-char base62 code (`crypto/rand`), writes `(code, long_url)` to Postgres,
and — if Redis is wired — pre-populates the cache (write-through). Returns
`{code, short_url, long_url}`. Random codes mean no auto-incrementing hot
row, which would otherwise be a write-side bottleneck.

### GET /:code    (hot read path)

Edge calls origin. Origin's `Cached` store checks Redis first:

- **Hit** — returns from Redis. One in-process allocation, no DB.
- **Negative hit** (sentinel value) — returns `ErrNotFound` immediately,
  so repeated 404s don't hammer the DB.
- **Miss** — `singleflight.Do` collapses concurrent misses on the same key
  into a single Postgres query, then populates Redis. This is the stampede
  defense: 1000 concurrent misses = 1 DB query, not 1000.

On success, edge returns a `302 Found` to the long URL. 302 (not 301)
because 301 is aggressively browser-cached and kills both analytics and
the ability to change a destination.

## Key design properties

- **Store interface** (`internal/storage/`) — handlers call `s.Store.Save`
  / `s.Store.Get`. The concrete type is chosen at startup (`memory`,
  `Postgres`, or `Cached` wrapping one of them). Zero handler code
  changes when backing changes.
- **Primitives live alone** (`internal/primitives/codegen/`) — pure
  functions, no I/O, no logging, no state. This is the module that will
  port to Python as `keystone-core` without rewriting.
- **Fail-fast startup** — Postgres and Redis are pinged at boot. Bad
  config crashes the process immediately rather than failing every
  request later.
- **Graceful shutdown** — SIGINT/SIGTERM waits up to 15s for in-flight
  requests to finish before exiting.
- **Explicit HTTP timeouts** — Go's stdlib default is "no timeout",
  which is a classic file-descriptor-exhaustion footgun. All three
  (Read, Write, Idle) are set.

## Env-driven composition

```
DATABASE_URL   REDIS_URL   store composition
─────────────  ──────────  ────────────────────────────
unset          unset       Memory              (dev only)
set            unset       Postgres            (durable, no cache)
set            set         Cached(Postgres)    (prod shape)
```

## What's still missing

Deliberate scope cuts for v0.1 — tracked here so they don't get lost:

- Edge-side caching (Workers KV). Right now every edge request falls
  through to origin.
- Click event pipeline (queue + analytics worker).
- Capability-scoped links (expiry, use count, scope).
- Provenance signing (Ed25519) — the primitive that justifies the
  Python-package extraction.
- Rate limiting on `/shorten`.
- Safe-Browsing / abuse checks at shorten time.
- Observability beyond stdout slog (OpenTelemetry traces, Prometheus
  metrics).
