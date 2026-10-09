// Package metrics — Prometheus instrumentation for Keystone.
//
// Design rules:
//
//   - Labels: NEVER raw URL paths or short codes (cardinality explosion
//     — a URL shortener with 1M codes would have 1M label values).
//     Use the chi route pattern ("/{code}") instead.
//   - Histograms: pick buckets to span the SLO targets. Our p99 target
//     on cache-hit is ~50ms, on cache-miss ~500ms. Buckets up to 2s.
//   - All metrics live in the default registry so promhttp.Handler()
//     exposes them without extra wiring.
//
// Metrics defined here are the full public surface; everything else
// reads from these.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTP — per request, labeled with the chi route pattern.
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "keystone_http_requests_total",
		Help: "Total HTTP requests served by the origin.",
	}, []string{"method", "route", "status"})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "keystone_http_request_duration_seconds",
		Help:    "HTTP request latency, seconds.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2},
	}, []string{"method", "route", "status"})

	// Cache — the Cached store wrapping the base Store.
	CacheLookups = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "keystone_cache_lookups_total",
		Help: "Cache lookups by outcome.",
	}, []string{"result"}) // hit | miss | miss_notfound | bypass_error

	// Singleflight — collapses concurrent misses.
	SingleflightShared = promauto.NewCounter(prometheus.CounterOpts{
		Name: "keystone_singleflight_shared_total",
		Help: "Number of lookups that joined an in-flight singleflight call.",
	})

	// Click pipeline.
	ClicksPublished = promauto.NewCounter(prometheus.CounterOpts{
		Name: "keystone_clicks_published_total",
		Help: "Click events published to the stream.",
	})
	ClicksConsumed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "keystone_clicks_consumed_total",
		Help: "Click events consumed and flushed to Postgres.",
	})
	ClickBatchSize = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "keystone_click_batch_size",
		Help:    "Number of events per consumer batch.",
		Buckets: []float64{1, 5, 10, 50, 100, 200, 500, 1000},
	})
	ClickStreamPending = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "keystone_click_stream_pending",
		Help: "Messages currently pending (XLEN) on the click stream.",
	})

	// Capability verification.
	CapabilityVerify = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "keystone_capability_verify_total",
		Help: "Capability token verification attempts by outcome.",
	}, []string{"result"}) // ok | bad_signature | expired | malformed | uses_exceeded | unknown_signer
)
