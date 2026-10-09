# Keystone — architecture (v0.1)

Short-link infrastructure for the agent era. This doc captures what the
code currently does, so a new reader (human or agent) can orient without
reading every file.

## Topology

```
   Client / Agent
        │
        ▼
  ┌─────────────────┐
  │   Edge Worker   │   TypeScript · Hono · Cloudflare Workers runtime
  │    (edge/)      │   localhost:8787 in dev, every CF PoP in prod
  └─────────────────┘
      │        │
    (hit)   (miss)
      │        │
      ▼        ▼
  ┌───────┐  ┌─────────────────┐             publish click (fire-and-forget)
  │Workers│  │   Go origin     │ ─────────────────────────────────────┐
  │  KV   │  │  cmd/server/    │   Go · chi · pgx · redis/go-redis    │
  │(edge  │  │  localhost:8080 │                                      │
  │ cache)│  └─────────────────┘                                      │
  └───────┘        │                                                  │
                   │ Store.Get / Save                                 │
              ┌────┴────┐                                             ▼
              ▼         ▼                                   ┌──────────────────┐
          ┌───────┐ ┌──────────┐                            │  Redis Stream    │
          │ Redis │ │ Postgres │                            │  keystone:clicks │
          │ cache │ │ (truth)  │                            └──────────────────┘
          └───────┘ └──────────┘                                     │
                                                                     │ XREADGROUP
                                                                     ▼
                                                            ┌──────────────────┐
                                                            │   clickworker    │ ← separate binary
                                                            │ cmd/clickworker/ │
                                                            └──────────────────┘
                                                                     │ batched UPSERT
                                                                     ▼
                                                               clicks table
                                                               (Postgres)
```

Two-tier cache: Workers KV at the edge, Redis at the origin. Edge misses
are rare (links are hot for hours); Redis misses are rarer still (links
are immutable); Postgres sees almost no read traffic once things warm up.

## Services

| Binary               | Purpose                                         |
|----------------------|-------------------------------------------------|
| `edge` (TS Worker)   | Hot-path redirect; KV + origin fallback.        |
| `cmd/server`         | Origin API: shorten + resolve.                  |
| `cmd/clickworker`    | Analytics consumer: Redis Stream → Postgres.    |

The analytics worker is a separate binary. If it crashes, the hot path
keeps serving. If it needs a deploy, the hot path doesn't. **Slow work
off the hot path** — the single most important shape in production
backends.

## Request paths

### POST /shorten (write)

Edge forwards JSON `{url}` to origin. Origin generates a random 7-char
base62 code (`crypto/rand`), writes `(code, long_url)` to Postgres via
the `Store` interface, pre-populates the Redis cache (write-through),
returns `{code, short_url, long_url}`. Edge then pre-warms Workers KV
via `ctx.waitUntil` — the first click of the new code will be an edge
hit, not a miss.

### GET /:code (hot read)

1. **Workers KV** read at the edge PoP. Hit → `302` immediately,
   ~5–15ms. No origin round-trip, no DB.
2. **Miss** → HTTP to origin.
3. Origin's `Cached` store checks Redis:
   - **Hit** → returns from Redis.
   - **Negative hit** → `ErrNotFound` immediately, shields DB from
     repeated 404s.
   - **Miss** → `singleflight.Do` collapses concurrent misses into one
     DB query, then populates Redis.
4. Origin publishes click event to Redis Stream, returns resolve JSON.
5. Edge populates KV via `ctx.waitUntil` so the next click is a hit,
   then issues `302` to the long URL.

302 (not 301) so the result stays uncached by browsers, keeping both
analytics and future destination changes intact.

### Click pipeline (async)

`clickworker` runs a loop:
1. `XREADGROUP` up to 500 messages, block up to 2s.
2. Aggregate in-memory by code → `map[string]int64`.
3. One UPSERT per unique code inside a transaction.
4. `XACK` the batch.

At-least-once delivery. Duplicate deliveries double-count within a batch
— acceptable for v1; exactly-once requires an idempotency key per event.

## Key design properties

- **Store interface** — handlers call `s.Store.Save` / `Get`. Concrete
  type is composed at startup. Zero handler changes to switch backing.
- **Primitives alone** (`internal/primitives/codegen/`) — pure functions,
  no I/O. Ports to Python as `keystone-core`.
- **Fail-fast startup** — Postgres and Redis are pinged at boot.
- **Graceful shutdown** — SIGINT/SIGTERM waits up to 15s for in-flight.
- **Explicit HTTP timeouts** — Read/Write/Idle all set.
- **Analytics is best-effort** — 100ms publish timeout; failures logged,
  never returned. Redirect must never block on it.
- **Edge writes are non-blocking** — `ctx.waitUntil` keeps KV writes
  going after the response ships.
- **Immutable codes** — codes never change destination in v0.1, so KV
  needs no invalidation. Immutability is the simplest cache coherency.

## Env-driven composition

```
DATABASE_URL   REDIS_URL   origin behavior                       clickworker
─────────────  ──────────  ────────────────────────────────────  ─────────────
unset          unset       Memory, no analytics                  —
set            unset       Postgres, no cache, no analytics       —
set            set         Cached(Postgres) + click publisher    runnable
```

## What's still missing

Deliberate scope cuts for v0.1 — tracked here so they don't get lost:

- Capability-scoped links (expiry, use count, scope).
- Provenance signing (Ed25519) — the primitive that justifies the
  Python package extraction.
- Rate limiting on `/shorten`.
- Safe-Browsing / abuse checks.
- Observability beyond stdout slog (OpenTelemetry traces, Prometheus metrics).
- Exactly-once click semantics (idempotency key per event).
- Multi-region active-passive origin + Postgres replication.
- Real Cloudflare KV namespace (currently a local Miniflare one).
