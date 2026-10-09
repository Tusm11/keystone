// Click consumer: reads events from the Redis Stream and batches them into
// Postgres. Separate goroutine → separate binary → separate deployment
// target in prod. The resolve hot path is not touched by any of this.
//
// Shape of the loop:
//
//   1. XREADGROUP, batch of up to BatchSize, blocking up to BlockDuration.
//   2. Aggregate the batch in-memory by code → map[string]int.
//   3. One SQL statement per unique code (INSERT ... ON CONFLICT DO UPDATE).
//   4. XACK the batch's ids.
//
// Idempotency: redelivery just double-counts within one batch. For strict
// exactly-once we'd carry an idempotency key per event (XID) and dedupe in
// Postgres. v1 skips that; the cost is minor count drift under failure.
package events

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const (
	GroupName     = "clickworker"
	BatchSize     = 500
	BlockDuration = 2 * time.Second
)

type Consumer struct {
	rdb      *redis.Client
	pool     *pgxpool.Pool
	consumer string // XREADGROUP consumer name — unique per worker instance
	log      *slog.Logger
}

func NewConsumer(rdb *redis.Client, pool *pgxpool.Pool, consumerName string, log *slog.Logger) *Consumer {
	return &Consumer{rdb: rdb, pool: pool, consumer: consumerName, log: log}
}

// EnsureGroup creates the consumer group if it doesn't exist. Safe to call
// at every startup — the "BUSYGROUP" error is swallowed.
func (c *Consumer) EnsureGroup(ctx context.Context) error {
	err := c.rdb.XGroupCreateMkStream(ctx, StreamName, GroupName, "0").Err()
	if err != nil && err.Error() == "BUSYGROUP Consumer Group name already exists" {
		return nil
	}
	return err
}

// Run blocks until ctx is cancelled, consuming and flushing batches.
func (c *Consumer) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		res, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    GroupName,
			Consumer: c.consumer,
			Streams:  []string{StreamName, ">"},
			Count:    BatchSize,
			Block:    BlockDuration,
		}).Result()
		if err != nil {
			if err == redis.Nil || ctx.Err() != nil {
				continue // timeout, no messages — poll again
			}
			c.log.Error("xreadgroup failed", "err", err)
			time.Sleep(1 * time.Second) // backoff on repeated errors
			continue
		}

		for _, stream := range res {
			if len(stream.Messages) == 0 {
				continue
			}
			if err := c.handleBatch(ctx, stream.Messages); err != nil {
				c.log.Error("batch handle failed", "err", err)
				// don't ack — redis will redeliver after visibility timeout
				continue
			}
			ids := make([]string, len(stream.Messages))
			for i, m := range stream.Messages {
				ids[i] = m.ID
			}
			if err := c.rdb.XAck(ctx, StreamName, GroupName, ids...).Err(); err != nil {
				c.log.Error("xack failed", "err", err, "count", len(ids))
			}
		}
	}
}

// handleBatch aggregates the batch and writes one row per unique code.
func (c *Consumer) handleBatch(ctx context.Context, msgs []redis.XMessage) error {
	counts := make(map[string]int64, len(msgs))
	for _, m := range msgs {
		code, ok := m.Values["code"].(string)
		if !ok || code == "" {
			continue // malformed event — drop silently
		}
		counts[code]++
	}
	if len(counts) == 0 {
		return nil
	}

	// A transaction so partial-flush can't leave the batch half-applied.
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Upsert each code's count. UPSERT is the right primitive here —
	// we never know if the row exists yet, and we don't want a round-trip
	// to find out. ON CONFLICT handles both insert and update in one shot.
	for code, delta := range counts {
		_, err := tx.Exec(ctx, `
			INSERT INTO clicks (code, count, last_click)
			VALUES ($1, $2, NOW())
			ON CONFLICT (code)
			DO UPDATE SET count = clicks.count + EXCLUDED.count,
			              last_click = EXCLUDED.last_click
		`, code, delta)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	c.log.Info("batch flushed", "codes", len(counts), "events", len(msgs))
	return nil
}
