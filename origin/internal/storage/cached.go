// Package storage — Redis-cached wrapper around another Store.
// See docs/architecture.md for the pattern (look-aside cache with
// singleflight stampede defense and negative caching).
package storage

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/Tusm11/keystone/origin/internal/metrics"
)

const (
	hitTTL         = 1 * time.Hour
	missTTL        = 30 * time.Second
	cachePrefix    = "keystone:code:"
	cacheOpTimeout = 100 * time.Millisecond
	missSentinel   = "__NOTFOUND__"
)

type Cached struct {
	inner Store
	rdb   *redis.Client
	sf    singleflight.Group
	log   *slog.Logger
}

func NewCached(inner Store, rdb *redis.Client, log *slog.Logger) *Cached {
	return &Cached{inner: inner, rdb: rdb, log: log}
}

func (c *Cached) Save(code, longURL string) error {
	if err := c.inner.Save(code, longURL); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cacheOpTimeout)
	defer cancel()
	if err := c.rdb.Set(ctx, cachePrefix+code, longURL, hitTTL).Err(); err != nil {
		c.log.Warn("cache set failed", "code", code, "err", err)
	}
	return nil
}

func (c *Cached) Get(code string) (string, error) {
	key := cachePrefix + code

	ctx, cancel := context.WithTimeout(context.Background(), cacheOpTimeout)
	defer cancel()
	val, err := c.rdb.Get(ctx, key).Result()
	switch {
	case err == nil && val == missSentinel:
		metrics.CacheLookups.WithLabelValues("miss_notfound").Inc()
		return "", ErrNotFound
	case err == nil:
		metrics.CacheLookups.WithLabelValues("hit").Inc()
		return val, nil
	case errors.Is(err, redis.Nil):
		// miss — fall through to singleflight
	default:
		metrics.CacheLookups.WithLabelValues("bypass_error").Inc()
		c.log.Warn("cache get failed; bypassing", "code", code, "err", err)
		return c.inner.Get(code)
	}

	// Singleflight: collapse concurrent misses to one DB call.
	// `shared` is true when THIS caller joined an in-flight call rather
	// than being the leader — a direct measurement of the defense.
	v, err, shared := c.sf.Do(key, func() (any, error) {
		longURL, err := c.inner.Get(code)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				bgCtx, bgCancel := context.WithTimeout(context.Background(), cacheOpTimeout)
				defer bgCancel()
				_ = c.rdb.Set(bgCtx, key, missSentinel, missTTL).Err()
			}
			return "", err
		}
		bgCtx, bgCancel := context.WithTimeout(context.Background(), cacheOpTimeout)
		defer bgCancel()
		if err := c.rdb.Set(bgCtx, key, longURL, hitTTL).Err(); err != nil {
			c.log.Warn("cache set after miss failed", "code", code, "err", err)
		}
		return longURL, nil
	})
	if shared {
		metrics.SingleflightShared.Inc()
	}
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			metrics.CacheLookups.WithLabelValues("miss_notfound").Inc()
		}
		return "", err
	}
	metrics.CacheLookups.WithLabelValues("miss").Inc()
	return v.(string), nil
}
