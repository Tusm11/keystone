# Keystone — observability

Every Go process exposes `/metrics` in Prometheus text format. Prometheus
scrapes all replicas via DNS service discovery; Grafana reads from
Prometheus and renders the dashboard.

## Topology

```
origin-1  ─┐
origin-2  ─┼→  Prometheus  ─→  Grafana  (http://localhost:3000)
origin-3  ─┘   (scrape 5s)
clickworker ─→
```

Prometheus's `dns_sd_configs` resolves the Docker service DNS entry for
`origin` to the A records of all three replicas, then scrapes each.
Scale up → new replica appears in DNS → scraped automatically.

## Running it

The scale profile and the observe profile stack:

```
docker compose --profile scale --profile observe up -d --build --scale origin=3
```

Then open:

- Grafana: http://localhost:3000  (anonymous viewer; admin / admin to edit)
- Prometheus: http://localhost:9090

The "Keystone — overview" dashboard loads automatically.

## Metric vocabulary

| Name | Type | Purpose |
|------|------|---------|
| `keystone_http_requests_total{method,route,status}` | counter | Request count. Labels use chi route patterns — never raw paths, to avoid cardinality explosion. |
| `keystone_http_request_duration_seconds{method,route,status}` | histogram | Latency; buckets 5ms → 2s. p99 computed via `histogram_quantile`. |
| `keystone_cache_lookups_total{result}` | counter | `hit` / `miss` / `miss_notfound` / `bypass_error`. Hit rate = hit / total. |
| `keystone_singleflight_shared_total` | counter | Times a lookup joined an in-flight singleflight call. Non-zero = defense worked. |
| `keystone_clicks_published_total` | counter | Click events pushed to the stream. |
| `keystone_clicks_consumed_total` | counter | Events the worker drained to Postgres. |
| `keystone_click_batch_size` | histogram | Events per batch (shows the amortization gain). |
| `keystone_click_stream_pending` | gauge | XLEN of the click stream. Growing without bound = consumer lagging. |
| `keystone_capability_verify_total{result}` | counter | `ok`, `bad_signature`, `expired`, `malformed`, `uses_exceeded`, `unknown_signer`. |

## The 9 dashboard panels and what they prove

1. **Requests / sec** — fleet throughput. Climbs as you load-test.
2. **p99 latency** — the SLO number.
3. **Cache hit rate** — settles above 95% once the working set is warm.
4. **Stream pending** — the consumer-lag canary.
5. **HTTP latency percentiles over time** — p50, p95, p99 together. The curve shape IS the system's behavior.
6. **Request rate by route** — see which endpoints move. Spikes in `/shorten` under load = write burst.
7. **Cache lookups by outcome** — stacked. Hits dominate; the thin "miss" slice is what singleflight protects.
8. **Singleflight shares + Click pipeline** — the architectural defenses in one chart. Singleflight shares spike exactly when cold-code bursts arrive. Published vs consumed should stay equal in steady state.
9. **Requests by replica** — spraying evenly across the fleet = load balancer working. One replica dropping to zero = failover event.

## PromQL cheat sheet for the specific proofs

Cache hit rate (last 1m):
```
sum(rate(keystone_cache_lookups_total{result="hit"}[1m]))
  / sum(rate(keystone_cache_lookups_total[1m]))
```

p99 request duration (last 1m):
```
histogram_quantile(0.99,
  sum(rate(keystone_http_request_duration_seconds_bucket[1m])) by (le))
```

Singleflight collapse rate:
```
rate(keystone_singleflight_shared_total[1m])
```

Clicks consumer lag (grows if worker falls behind):
```
keystone_click_stream_pending
```

Fleet balance across replicas:
```
sum(rate(keystone_http_requests_total[1m])) by (instance)
```

## What to screenshot for the portfolio

Run the scale+observe stack. Hit it with k6 for 60 seconds. Open Grafana.
The dashboard tells the story in one picture:

- Requests per second climbing to ~2k.
- p99 holding under 100ms.
- Cache hit rate rising to ~99%.
- Three equal lines in the "Requests by replica" panel.
- Published vs consumed clicks staying balanced.

That image is more credible than any architecture doc.
