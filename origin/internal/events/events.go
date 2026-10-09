// Package events — click event pipeline over Redis Streams.
// Spec and guarantees in docs/architecture.md.
package events

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Tusm11/keystone/origin/internal/metrics"
)

const (
	StreamName     = "keystone:clicks"
	StreamMaxLen   = 1_000_000
	publishTimeout = 100 * time.Millisecond
)

type Publisher struct {
	rdb *redis.Client
	log *slog.Logger
}

func NewPublisher(rdb *redis.Client, log *slog.Logger) *Publisher {
	return &Publisher{rdb: rdb, log: log}
}

func (p *Publisher) PublishClick(code string) {
	if p == nil {
		return
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
		return
	}
	metrics.ClicksPublished.Inc()
}
