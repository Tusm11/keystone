// Package ratelimit — distributed fixed-window rate limiting via Redis.
//
// Why a Lua script, not two Redis commands:
//
//   The naive version is INCR followed by EXPIRE. But between INCR and
//   EXPIRE, another process could race in and the key could end up with
//   NO expiry — permanent counter, limiter stuck returning "blocked"
//   forever. Running INCR+EXPIRE inside a single Lua script makes them
//   atomic from Redis's perspective; nothing can interleave.
//
//   This is the archetypal "I need multiple Redis ops to happen as one"
//   pattern. Lua scripts in Redis are the right answer.
//
// Why fixed-window and not sliding or GCRA:
//
//   Fixed-window is one counter per key per window. Simple, cheap,
//   boundaries allow bursts (2× limit possible around the second-tick).
//   For abuse protection (not SLA-level fairness) that burst allowance
//   is fine. GCRA / sliding log is overkill at this scope.
//
// Fail-open on Redis errors:
//
//   If Redis is unreachable, Allow returns `true` and logs. Rationale:
//   a dead rate limiter should not turn into a global 503. The downside
//   is attackers could DOS Redis to bypass — a tradeoff documented and
//   measurable via a Prometheus counter.

package ratelimit

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Tusm11/keystone/origin/internal/metrics"
)

const keyPrefix = "keystone:rl:"

// Returns {allowed:int (0|1), retry_after_seconds:int}
var luaScript = redis.NewScript(`
local key    = KEYS[1]
local limit  = tonumber(ARGV[1])
local window = tonumber(ARGV[2])

local count = redis.call('INCR', key)
if count == 1 then
    -- Only set TTL on first increment of this window — don't reset on repeats
    redis.call('EXPIRE', key, window)
end
if count > limit then
    local ttl = redis.call('TTL', key)
    if ttl < 0 then ttl = window end
    return {0, ttl}
end
return {1, 0}
`)

type Limiter struct {
	rdb *redis.Client
	log *slog.Logger
}

func New(rdb *redis.Client, log *slog.Logger) *Limiter {
	return &Limiter{rdb: rdb, log: log}
}

// Allow checks whether `scope:ident` has budget left. Returns the
// decision + Retry-After seconds (0 when allowed). Fail-open on error.
func (l *Limiter) Allow(ctx context.Context, scope, ident string, limit int, window time.Duration) (bool, int) {
	if l == nil || l.rdb == nil {
		return true, 0
	}
	key := keyPrefix + scope + ":" + ident

	callCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	res, err := luaScript.Run(callCtx, l.rdb, []string{key}, limit, int(window.Seconds())).Result()
	if err != nil {
		// Redis unreachable or slow — fail open. See package docstring.
		l.log.Warn("rate-limit redis error; failing open", "key", key, "err", err)
		metrics.RateLimitDecisions.WithLabelValues(scope, "bypass_error").Inc()
		return true, 0
	}
	arr, ok := res.([]any)
	if !ok || len(arr) != 2 {
		l.log.Warn("rate-limit unexpected lua result", "res", res)
		return true, 0
	}
	allowed, _ := arr[0].(int64)
	retry, _ := arr[1].(int64)
	if allowed == 1 {
		metrics.RateLimitDecisions.WithLabelValues(scope, "allowed").Inc()
		return true, 0
	}
	metrics.RateLimitDecisions.WithLabelValues(scope, "blocked").Inc()
	return false, int(retry)
}

// ErrDisabled is returned by NewFromEnvOrDisabled when the user has
// explicitly opted out of rate limiting.
var ErrDisabled = errors.New("rate limiting disabled")
