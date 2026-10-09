// Package storage — Redis-cached wrapper around another Store.
//
// Cached implements Store by consulting Redis first on reads; on miss it
// falls through to the underlying Store (Postgres in prod), caches the
// result, and returns it. Writes go to the underlying Store first, then
// populate the cache (write-through). This is the standard "look-aside
// cache" pattern.
//
// Two production concerns are handled here:
//
//   1. Cache stampede. When a popular key expires, hundreds of concurrent
//      requests all miss at once and all try to hit the DB. We use
//      singleflight so only ONE of them actually queries; the others wait
//      for its result. One DB query per missed key, no matter the concurrency.
//
//   2. Negative caching. We cache "not found" for a short TTL too, so that
//      repeated requests for nonexistent codes don't hammer the DB. Shorter
//      TTL than hits, because a 404 can become a 200 the moment someone
//      creates that code.
package storage

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	hitTTL          = 1 * time.Hour       // positive cache: 1h is plenty for immutable links
	missTTL         = 30 * time.Second    // negative cache: short, in case the code gets created
	cachePrefix     = "keystone:code:"    // namespace Redis keys so we can share Redis later
	cacheOpTimeout  = 100 * time.Millisecond
	missSentinel    = "__NOTFOUND__"      // sentinel value for negative cache entries
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
	// Write-through: durable store first, then cache. If the cache write
	// fails, the mapping is still correct — just not pre-warmed.
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

	// 1. Try cache.
	ctx, cancel := context.WithTimeout(context.Background(), cacheOpTimeout)
	defer cancel()
	val, err := c.rdb.Get(ctx, key).Result()
	switch {
	case err == nil && val == missSentinel:
		return "", ErrNotFound
	case err == nil:
		return val, nil
	case errors.Is(err, redis.Nil):
		// genuine cache miss — fall through
	default:
		// Redis is down or slow. Log and bypass to the inner store rather
		// than failing the request. Cache is an optimization, not a dep.
		c.log.Warn("cache get failed; bypassing", "code", code, "err", err)
		return c.inner.Get(code)
	}

	// 2. Cache miss — singleflight collapses N concurrent misses on the same
	//    key into ONE call to the inner store. This is the stampede defense.
	v, err, _ := c.sf.Do(key, func() (any, error) {
		longURL, err := c.inner.Get(code)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				// Negative cache: short TTL so a later create recovers quickly.
				bgCtx, bgCancel := context.WithTimeout(context.Background(), cacheOpTimeout)
				defer bgCancel()
				_ = c.rdb.Set(bgCtx, key, missSentinel, missTTL).Err()
			}
			return "", err
		}
		// Positive cache.
		bgCtx, bgCancel := context.WithTimeout(context.Background(), cacheOpTimeout)
		defer bgCancel()
		if err := c.rdb.Set(bgCtx, key, longURL, hitTTL).Err(); err != nil {
			c.log.Warn("cache set after miss failed", "code", code, "err", err)
		}
		return longURL, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}
