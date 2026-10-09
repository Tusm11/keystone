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
  ┌──────────────┐           publish click (fire-and-forget)
  │  Go origin   │ ────────────────────────────────────────┐
  │ cmd/server/  │   Go · chi · pgx · redis/go-redis       │
  │              │   localhost:8080 in dev                 │
  └──────────────┘                                         │
        │ Store.Get / Save                                 │
   ┌────┴────┐                                             ▼
   ▼         ▼                                   ┌──────────────────┐
┌───────┐ ┌──────────┐                           │  Redis Stream    │
│ Redis │ │ Postgres │                           │  keystone:clicks │
│ cache │ │ (truth)  │                           └──────────────────┘
└───────┘ └──────────┘                                     │
                                                           │ XREADGROUP
                                                           ▼
                                                  ┌──────────────────┐
                                                  │   clickworker    │ ← separate binary
                                                  │ cmd/clickworker/ │   go run ./cmd/clickworker
                                                  └──────────────────┘
                                                           │ batched UPSERT
                                                           ▼
                                                     clicks table
                                                     (Postgres)
```

## Services

| Binary               | Purpose                                         |
|----------------------|-------------------------------------------------|
| `edge` (TS Worker)   | Hot-path redirect; thin HTTP proxy to origin.   |
| `cmd/server`         | Origin API: shorten + resolve.                  |
| `cmd/clickworker`    | Analytics consumer: Redis Stream → Postgres.    |

The analytics worker is deliberately a separate binary. If it crashes,
the hot path keeps serving. If it needs a deploy, the hot path doesn't.
This is the single most important shape in production backends: **slow
work off the hot path**.

## Request paths

### POST /shorten (write)

Edge forwards JSON `{url}` to origin. Origin generates a random 7-char
base62 code (`crypto/rand`), writes `(code, long_url)` to Postgres via
the `Store` interface, pre-populates the Redis cache (write-through),
returns `{code, short_url, long_url}`. Random codes avoid an
auto-incrementing hot row.

### GET /:code (hot read)

Edge calls origin. `Cached` store consults Redis:

- **Hit** → return from Redis. ~1 allocation, no DB.
- **Negative hit** (sentinel value) → `ErrNotFound` immediately, shields
  DB from repeated 404s.
- **Miss** → `singleflight.Do` collapses concurrent misses on the same
  key into one DB query, then populates Redis. Textbook stampede defense.

On success, origin publishes a click event to Redis Stream
(`XADD keystone:clicks`), returns the resolve JSON. Edge issues a `302
Found` to the long URL. 302 (not 301) so the result stays uncached by
browsers, which keeps analytics and future-destination-change intact.

### Click pipeline (async)

`clickworker` runs a loop:
1. `XREADGROUP` up to 500 messages, block up to 2s.
2. Aggregate in-memory by code → `map[string]int64`.
3. One UPSERT per unique code inside a transaction
   (`INSERT ... ON CONFLICT DO UPDATE`).
4. `XACK` the batch.

Guarantees: at-least-once delivery. Duplicate deliveries double-count
within a batch — acceptable for v1; exactly-once requires an idempotency
key per event, deferred.

## Key design properties

- **Store interface** — handlers call `s.Store.Save` / `Get`. Concrete
  type is composed at startup: `Memory`, `Postgres`, or `Cached` wrapping
  one of them. Zero handler changes to switch.
- **Primitives alone** (`internal/primitives/codegen/`) — pure functions,
  no I/O. This is the module that will port to Python as `keystone-core`.
- **Fail-fast startup** — Postgres and Redis are pinged at boot.
- **Graceful shutdown** — SIGINT/SIGTERM waits up to 15s for in-flight
  requests.
- **Explicit HTTP timeouts** — Read/Write/Idle all set. Go's stdlib
  default is "no timeout", a classic fd-exhaustion footgun.
- **Analytics is best-effort** — publish uses 100ms timeout and logs on
  failure, never returns an error. Redirect must never block on it.

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

- Edge-side caching (Workers KV).
- Capability-scoped links (expiry, use count, scope).
- Provenance signing (Ed25519).
- Rate limiting on `/shorten`.
- Safe-Browsing / abuse checks.
- Observability beyond stdout slog (OpenTelemetry traces, Prometheus metrics).
- Exactly-once click semantics (idempotency key per event).
- Multi-region active-passive origin + Postgres replication.
