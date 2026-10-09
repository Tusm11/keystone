// Click consumer: reads events from the Redis Stream, aggregates by code,
// writes one transactional UPSERT per batch to Postgres.
package events

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/Tusm11/keystone/origin/internal/metrics"
)

const (
	GroupName     = "clickworker"
	BatchSize     = 500
	BlockDuration = 2 * time.Second
	pendingPollEvery = 10 * time.Second
)

type Consumer struct {
	rdb      *redis.Client
	pool     *pgxpool.Pool
	consumer string
	log      *slog.Logger
}

func NewConsumer(rdb *redis.Client, pool *pgxpool.Pool, consumerName string, log *slog.Logger) *Consumer {
	return &Consumer{rdb: rdb, pool: pool, consumer: consumerName, log: log}
}

func (c *Consumer) EnsureGroup(ctx context.Context) error {
	err := c.rdb.XGroupCreateMkStream(ctx, StreamName, GroupName, "0").Err()
	if err != nil && err.Error() == "BUSYGROUP Consumer Group name already exists" {
		return nil
	}
	return err
}

func (c *Consumer) Run(ctx context.Context) error {
	// Background pending-count poller feeds the gauge for Grafana.
	go c.pollPending(ctx)

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
				continue
			}
			c.log.Error("xreadgroup failed", "err", err)
			time.Sleep(1 * time.Second)
			continue
		}
		for _, stream := range res {
			if len(stream.Messages) == 0 {
				continue
			}
			if err := c.handleBatch(ctx, stream.Messages); err != nil {
				c.log.Error("batch handle failed", "err", err)
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

func (c *Consumer) pollPending(ctx context.Context) {
	t := time.NewTicker(pendingPollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := c.rdb.XLen(ctx, StreamName).Result()
			if err != nil {
				continue
			}
			metrics.ClickStreamPending.Set(float64(n))
		}
	}
}

func (c *Consumer) handleBatch(ctx context.Context, msgs []redis.XMessage) error {
	counts := make(map[string]int64, len(msgs))
	for _, m := range msgs {
		code, ok := m.Values["code"].(string)
		if !ok || code == "" {
			continue
		}
		counts[code]++
	}
	if len(counts) == 0 {
		return nil
	}

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

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

	metrics.ClicksConsumed.Add(float64(len(msgs)))
	metrics.ClickBatchSize.Observe(float64(len(msgs)))
	c.log.Info("batch flushed", "codes", len(counts), "events", len(msgs))
	return nil
}
