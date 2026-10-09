// Package events — click event pipeline over Redis Streams.
//
// Why Redis Streams for this: we already have Redis for the cache; Streams
// give us persistent FIFO with consumer groups (XREADGROUP + XACK), which
// is "classic queue" semantics — at-least-once delivery with manual ack.
// In production the exact same shape maps to Cloudflare Queues or Kafka;
// swap the Publisher/Consumer implementation and the stream name is still
// the contract.
//
// Guarantees:
//   - Publish is bounded: XADD with MAXLEN ~ to cap stream size. Writes
//     past the cap evict the oldest ids. Analytics loss > unbounded growth.
//   - Deliver is at-least-once. Our consumer idempotently upserts counts
//     (INSERT ... ON CONFLICT DO UPDATE), so duplicate deliveries just
//     double-count the same row for that batch — acceptable for v1.
//   - Publish never blocks the resolve: 100ms timeout, and failures are
//     logged, not returned. Analytics being down must not break redirects.
package events

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	StreamName       = "keystone:clicks"
	StreamMaxLen     = 1_000_000  // ~ cap: approximate, cheap for Redis
	publishTimeout   = 100 * time.Millisecond
)

type Publisher struct {
	rdb *redis.Client
	log *slog.Logger
}

func NewPublisher(rdb *redis.Client, log *slog.Logger) *Publisher {
	return &Publisher{rdb: rdb, log: log}
}

// PublishClick records a click for `code`. Non-blocking (short timeout);
// failures are logged, never returned — a dead analytics path must not
// break the redirect.
func (p *Publisher) PublishClick(code string) {
	if p == nil {
		return // no-op when publisher not wired (dev without Redis)
	}
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()
	err := p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamName,
		MaxLen: StreamMaxLen,
		Approx: true,
		Values: map[string]any{
			"code": code,
			"ts":   time.Now().UnixMilli(),
		},
	}).Err()
	if err != nil {
		p.log.Warn("click publish failed", "code", code, "err", err)
	}
}
