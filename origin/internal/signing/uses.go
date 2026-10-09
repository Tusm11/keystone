// Package signing — max-uses enforcement for capability tokens.
//
// The token itself does NOT track usage (per the v1 spec). An external
// counter does, keyed by the capability nonce. We use Redis INCR, which
// is atomic — safe under concurrent redeems.

package signing

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrUsesExceeded = errors.New("signing: capability max_uses exceeded")

const (
	usesKeyPrefix = "keystone:cap:uses:"
	usesOpTimeout = 100 * time.Millisecond
	usesTTL       = 7 * 24 * time.Hour // forget nonces older than a week
)

type UsesCounter struct {
	rdb *redis.Client
}

func NewUsesCounter(rdb *redis.Client) *UsesCounter {
	return &UsesCounter{rdb: rdb}
}

// Charge atomically increments the counter for `nonce`. If `max == 0`
// (unlimited), the counter is still bumped but never rejects. If
// `max > 0`, the Nth charge where N > max returns ErrUsesExceeded.
//
// Non-blocking on Redis errors: if Redis is unreachable, Charge returns
// nil (fail-open) rather than blocking real redirects. A dead counter
// means uses enforcement is temporarily off, not that resolves break.
// Fail-closed is the alternative; v1 prefers availability.
func (u *UsesCounter) Charge(nonce string, max int64) error {
	if u == nil || u.rdb == nil {
		return nil // uses enforcement disabled
	}
	ctx, cancel := context.WithTimeout(context.Background(), usesOpTimeout)
	defer cancel()

	key := usesKeyPrefix + nonce
	count, err := u.rdb.Incr(ctx, key).Result()
	if err != nil {
		// Fail-open — see docstring.
		return nil
	}
	// Set TTL only on first increment (count == 1).
	if count == 1 {
		_ = u.rdb.Expire(ctx, key, usesTTL).Err()
	}
	if max > 0 && count > max {
		return ErrUsesExceeded
	}
	return nil
}
