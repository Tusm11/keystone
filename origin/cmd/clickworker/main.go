// clickworker: consumes click events from the Redis Stream and batches
// them into the Postgres clicks table. Also exposes a /metrics endpoint
// on :8081 so Prometheus can scrape its own stats.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/Tusm11/keystone/origin/internal/events"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	dsn := os.Getenv("DATABASE_URL")
	redisURL := os.Getenv("REDIS_URL")
	if dsn == "" || redisURL == "" {
		log.Error("clickworker requires DATABASE_URL and REDIS_URL")
		os.Exit(1)
	}
	metricsAddr := envOr("CLICKWORKER_METRICS_ADDR", ":8081")

	hostname, _ := os.Hostname()
	consumerName := fmt.Sprintf("%s-%d", hostname, os.Getpid())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Error("postgres connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	pingCtx, c1 := context.WithTimeout(ctx, 5*time.Second)
	defer c1()
	if err := pool.Ping(pingCtx); err != nil {
		log.Error("postgres ping failed", "err", err)
		os.Exit(1)
	}
	log.Info("connected to postgres")

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Error("REDIS_URL parse failed", "err", err)
		os.Exit(1)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()
	pingCtx2, c2 := context.WithTimeout(ctx, 3*time.Second)
	defer c2()
	if err := rdb.Ping(pingCtx2).Err(); err != nil {
		log.Error("redis ping failed", "err", err)
		os.Exit(1)
	}
	log.Info("connected to redis")

	consumer := events.NewConsumer(rdb, pool, consumerName, log)
	if err := consumer.EnsureGroup(ctx); err != nil {
		log.Error("ensure group failed", "err", err)
		os.Exit(1)
	}

	// Metrics server on its own port — separate from any business API.
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		log.Info("clickworker metrics listening", "addr", metricsAddr)
		if err := http.ListenAndServe(metricsAddr, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server failed", "err", err)
		}
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Info("shutting down")
		cancel()
	}()

	log.Info("clickworker started", "consumer", consumerName)
	if err := consumer.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("consumer stopped", "err", err)
		os.Exit(1)
	}
	log.Info("stopped")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
