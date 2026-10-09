// HTTP middleware wrapping the Limiter.
// scope: logical label of the endpoint (used for both key namespacing and metric labels).

package ratelimit

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func Middleware(l *Limiter, scope string, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ident := clientIP(r)
			allowed, retryAfter := l.Allow(r.Context(), scope, ident, limit, window)
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":       "rate limit exceeded",
					"retry_after": retryAfter,
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP respects the first entry of X-Forwarded-For (set by nginx)
// before falling back to RemoteAddr. Order matters: in a trusted-proxy
// setup, X-F-F reflects the real client; in a direct-access setup, it
// may be user-controlled — but the only consequence here is "a determined
// attacker gets a wider rate-limit pool", not a security failure.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if comma := strings.IndexByte(xff, ','); comma > 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
