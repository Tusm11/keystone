// keystone-origin: the authoritative shortener API.
//
// Backing store is chosen at startup from env vars:
//
//   DATABASE_URL + REDIS_URL  → Cached(Postgres) + click publisher   (prod)
//   DATABASE_URL only         → Postgres, no cache, no analytics
//   neither                   → in-memory, no analytics              (dev)
//
// Handlers depend only on the Store interface + an optional Publisher.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Tusm11/keystone/origin/internal/events"
	keystonehttp "github.com/Tusm11/keystone/origin/internal/http"
	"github.com/Tusm11/keystone/origin/internal/storage"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	addr := envOr("ORIGIN_ADDR", ":8080")

	store, publisher := build(log)
	server := &keystonehttp.Server{Store: store, Clicks: publisher}

	// Explicit timeouts — the Go stdlib default is "no timeout", a classic
	// fd-exhaustion footgun.
	s := &http.Server{
		Addr:         addr,
		Handler:      server.Routes(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	done := make(chan struct{})
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		<-sigs
		log.Info("shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			log.Error("shutdown error", "err", err)
		}
		close(done)
	}()

	log.Info("keystone-origin listening", "addr", addr)
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
	<-done
	log.Info("stopped")
}

func build(log *slog.Logger) (storage.Store, *events.Publisher) {
	dsn := os.Getenv("DATABASE_URL")

	var base storage.Store
	if dsn == "" {
		log.Warn("DATABASE_URL unset — using in-memory store (dev only)")
		base = storage.NewMemory()
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pg, err := storage.NewPostgres(ctx, dsn)
		if err != nil {
			log.Error("postgres connect failed", "err", err)
			os.Exit(1)
		}
		log.Info("connected to postgres")
		base = pg
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		return base, nil
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Error("REDIS_URL parse failed", "err", err)
		os.Exit(1)
	}
	rdb := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		log.Error("redis ping failed", "err", err)
		os.Exit(1)
	}
	log.Info("connected to redis — cache + analytics enabled")

	cached := storage.NewCached(base, rdb, log)
	publisher := events.NewPublisher(rdb, log)
	return cached, publisher
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
