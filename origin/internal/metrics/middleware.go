// HTTP middleware that records request count + duration against the
// chi route pattern (not the raw URL — critical for low cardinality).

package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// statusCapture lets the middleware observe the response status without
// forcing handlers to use a non-stdlib ResponseWriter.
type statusCapture struct {
	http.ResponseWriter
	status int
}

func (s *statusCapture) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusCapture{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		// chi exposes the matched route pattern (e.g. "/{code}") through
		// its route context — this is the right label for cardinality.
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unknown"
		}
		status := strconv.Itoa(rec.status)
		elapsed := time.Since(start).Seconds()

		HTTPRequestsTotal.WithLabelValues(r.Method, route, status).Inc()
		HTTPRequestDuration.WithLabelValues(r.Method, route, status).Observe(elapsed)
	})
}
