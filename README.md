# Keystone

> Short-link infrastructure for the agent era. Provenance-signed
> capability tokens, horizontal-scale Go origin, TypeScript edge, and
> a Python verifier (`keystone-core`) with cross-language test vectors.

Keystone is a URL shortener built as a vehicle for serious systems
engineering. The redirect is the pretext; the real work is the stack
behind it: layered caching, singleflight stampede defense, an async
event pipeline, horizontal scaling, observability, rate limiting, and
Ed25519-signed capability tokens that any client can verify in any
language given only the public key.

---

## Status

Alpha. All subsystems work; the Python package is pre-1.0 so the wire
format spec may still change. Measured, tested, and documented end-to-end.

---

## Measured, not asserted

All numbers are from a single laptop running Docker Desktop, 100
concurrent VUs, 30-second measurement window, driven by `k6`. See
`loadtest/` for scripts and `docs/architecture.md` for the context.

| Measurement | Value | What it proves |
|---|---|---|
| Origin-direct throughput | **2,101 req/s** | Go stack + Redis cache + pgxpool |
| Origin-direct p99 latency | **59 ms** | Explicit timeouts, goroutine scheduling |
| HTTP failure rate | **0.00%** (99,344 / 99,344) | No connection errors under sustained load |
| Load-balanced p99 (3 replicas) | **97 ms** | nginx least_conn + Docker DNS re-resolve |
| Stampede collapse | **1 Postgres SELECT per 200 concurrent cold-key hits** | `singleflight.Do` |
| Click aggregation | **1 UPSERT per 200 click events** | Redis Streams consumer-group batching |
| Fleet uptime under replica kill | **continuous** | Nginx health check + stateless replicas |

Local-dev numbers are dominated by Docker Desktop's network overhead on
Windows (`host.docker.internal` through WSL2 adds ~30–50ms per hop).
Production on bare Linux + Cloudflare edge would be several times
faster. The ratios above are honest; the absolute numbers are a floor.

---

## Architecture at a glance

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
  ┌───────┐  ┌─────────────────┐          publish click (fire-and-forget)
  │Workers│  │   Go origin     │ ─────────────────────────────────┐
  │  KV   │  │  (cmd/server)   │   Go · chi · pgx · redis         │
  │(edge  │  │  localhost:8080 │                                  │
  │ cache)│  └─────────────────┘                                  │
  └───────┘       │                                               │
                  │ Store.Get / Save                              │
             ┌────┴────┐                                          ▼
             ▼         ▼                                  ┌──────────────────┐
         ┌───────┐ ┌──────────┐                           │  Redis Stream    │
         │ Redis │ │ Postgres │                           │  keystone:clicks │
         │ cache │ │ (truth)  │                           └──────────────────┘
         └───────┘ └──────────┘                                   │ XREADGROUP
                                                                  ▼
                                                         ┌──────────────────┐
                                                         │   clickworker    │ ← separate binary
                                                         │ (cmd/clickworker)│
                                                         └──────────────────┘
                                                                  │ batched UPSERT
                                                                  ▼
                                                            clicks table
                                                            (Postgres)
```

**Two-tier cache**: Workers KV at the edge, Redis at the origin. The
hot path serves most reads from the nearest edge PoP without ever
touching the origin.

**Capability tokens** (optional per link): a `GET /:code?k=<token>`
goes through verification before resolving. Tokens are Ed25519-signed,
decoded locally — no round-trip. Spec in `docs/capability-spec.md`.

Full architecture: `docs/architecture.md`.

---

## Quick start

Prereqs: Docker, Node 20+, Go 1.23+.

### 1. Base services

```bash
git clone https://github.com/<your-org>/keystone.git
cd keystone
docker compose up -d                # postgres + redis
```

### 2. Native dev loop (fast iteration)

```bash
# Terminal 1 — origin
cd origin
export DATABASE_URL=postgres://keystone:keystone_dev_pw@localhost:5432/keystone
export REDIS_URL=redis://localhost:6379/0
go run ./cmd/server

# Terminal 2 — click worker
cd origin
go run ./cmd/clickworker

# Terminal 3 — edge
cd edge
npm install
npm run dev
```

Visit `http://localhost:8787/health` → `{"status":"ok","service":"keystone-edge"}`.

### 3. Try it

```bash
# shorten
curl -X POST http://127.0.0.1:8787/shorten \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com"}'

# follow (302 to example.com)
curl -I http://127.0.0.1:8787/<code>
```

### 4. Full production shape

```bash
docker compose --profile scale --profile observe up -d --build --scale origin=3
```

That boots: 3 origin replicas, 1 clickworker, nginx LB on `:8090`,
Prometheus on `:9090`, Grafana on `:3000`.

Grafana ships with a pre-provisioned "Keystone — overview" dashboard
(10 panels covering throughput, p99, cache hit rate, singleflight
rate, click pipeline health, per-replica request distribution,
rate-limit decisions).

See `docs/scaling.md` and `docs/observability.md` for the operator view.

### 5. Load tests

```bash
# Hit origin direct for the honest Go-stack numbers
docker compose --profile loadtest run --rm \
  -e BASE_URL=http://host.docker.internal:8080 \
  k6 run scripts/read-warm.js
```

Full suite in `loadtest/README.md`: `read-warm`, `read-cold`,
`stampede` (prove singleflight), `mixed` (realistic 95% read / 5%
write).

---

## What's in the repo

```
keystone/
├── edge/                        TypeScript Worker (Hono)
├── origin/                      Go backend
│   ├── cmd/
│   │   ├── server/              Origin API — shorten + resolve + capabilities
│   │   ├── clickworker/         Analytics consumer (separate binary)
│   │   ├── keygen/              Ed25519 keypair generator
│   │   └── gentestvectors/      Produces cross-language test vectors
│   ├── internal/
│   │   ├── primitives/
│   │   │   ├── capability/      Signed capability tokens (v1 spec)
│   │   │   └── codegen/         base62 code generator
│   │   ├── signing/             Signer registry + Redis-backed uses counter
│   │   ├── storage/             Store interface + Memory/Postgres/Cached
│   │   ├── events/              Redis Streams publisher + consumer
│   │   ├── ratelimit/           Lua-script distributed limiter
│   │   ├── metrics/             Prometheus instruments + middleware
│   │   └── http/                Chi handlers wiring everything together
│   └── migrations/              Postgres schema (auto-applied on volume init)
├── infra/
│   ├── lb/nginx.conf            Load balancer config
│   ├── prometheus/              Scrape config (DNS service discovery)
│   └── grafana/                 Pre-provisioned dashboard + datasource
├── loadtest/                    k6 scenarios
├── packages/
│   └── python/keystone-core/    PyPI-ready Python port of the primitives
└── docs/                        Specs + architecture + operator guides
```

---

## Deeper reading

Everything below is in-repo:

- **`docs/architecture.md`** — topology, request paths, design properties
- **`docs/capability-spec.md`** — v1 wire format for signed tokens (authoritative)
- **`docs/scaling.md`** — four stateless invariants, LB mechanics, failover
- **`docs/observability.md`** — every metric explained + PromQL cheat sheet
- **`docs/rate-limiting.md`** — Lua script + fail-open rationale
- **`docs/test-vectors/capability.json`** — shared fixtures for Go↔Python interop
- **`loadtest/README.md`** — scenarios, thresholds, how to interpret
- **`packages/python/keystone-core/README.md`** — the Python package

---

## The Python package — `keystone-core`

The `capability` and `codegen` primitives are kept pure in Go and
ported to Python. The same wire-format spec drives both. The two
implementations are verified against each other via shared test
vectors: generate a token in Go, verify it byte-for-byte in Python,
and vice versa.

```bash
pip install keystone-core  # (coming to PyPI)
```

```python
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from keystone_core import Capability, verify_capability

priv = Ed25519PrivateKey.generate()
pub = priv.public_key()
cap = Capability(code="aB3xZ9k", scope="read-only", signer="my-service", uses=5)
token = cap.sign(priv)

# Elsewhere, with only the public key:
verified = verify_capability(token, pub)
```

Why this matters: for agent-to-agent link sharing, the receiving
agent needs to validate a link without calling back to the issuer. A
locally-verifiable signed token does that.

---

## Development

### Run all Go tests

```bash
cd origin
go test ./...
```

Covers primitives, cross-language vectors, rate limiter (when `REDIS_URL`
is set), storage, and signing.

### Run all Python tests

```bash
cd packages/python/keystone-core
pip install -e ".[dev]"
python -m pytest tests/ -v
```

23 tests, including 3 cross-language vector tests that verify Python
produces byte-identical tokens to Go for every fixture.

### Regenerate test vectors

If you change the wire-format spec, regenerate the vectors so both test
suites update:

```bash
go run ./origin/cmd/gentestvectors > docs/test-vectors/capability.json
```

Then run both test suites; any drift from the new spec surfaces as a
failed test.

---

## Author

Built by [Abhiram](https://github.com/Tusm11). Direct feedback welcome
via Issues.
