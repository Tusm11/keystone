package ratelimit

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// These tests only run when REDIS_URL points at a reachable Redis.
// They exist mainly to document the Lua script's expected behavior;
// skipped when no Redis is around (keeps `go test ./...` green in CI).
func newLimiter(t *testing.T) *Limiter {
	t.Helper()
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL not set; skipping Redis-backed limiter test")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rdb := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("redis unreachable: %v", err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	return New(rdb, log)
}

func TestAllow_UnderLimit(t *testing.T) {
	l := newLimiter(t)
	// Isolate — use a fresh ident per test to avoid cross-run pollution.
	ident := "test-under-" + time.Now().Format("150405.000000000")
	for i := 0; i < 5; i++ {
		ok, _ := l.Allow(context.Background(), "test", ident, 5, time.Minute)
		if !ok {
			t.Fatalf("iter %d: expected allow, got block", i)
		}
	}
}

func TestAllow_BlockedAfterLimit(t *testing.T) {
	l := newLimiter(t)
	ident := "test-over-" + time.Now().Format("150405.000000000")
	for i := 0; i < 3; i++ {
		ok, _ := l.Allow(context.Background(), "test", ident, 3, time.Minute)
		if !ok {
			t.Fatalf("iter %d: expected allow, got block", i)
		}
	}
	ok, retry := l.Allow(context.Background(), "test", ident, 3, time.Minute)
	if ok {
		t.Fatal("expected block, got allow")
	}
	if retry <= 0 || retry > 60 {
		t.Errorf("retry-after out of range: %d", retry)
	}
}
