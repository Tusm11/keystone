// Package storage — Postgres implementation of Store.
// Uses pgx's pgxpool for a managed connection pool (right default for a
// server; sql.DB works too but pgx is faster and has first-class Postgres
// features like LISTEN/NOTIFY we'll want later).
package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres opens a pool at dsn and pings it to fail-fast on bad config.
// dsn example: postgres://keystone:keystone_dev_pw@localhost:5432/keystone
func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	// Sensible pool defaults — small enough for a dev box, big enough that
	// burst traffic doesn't instantly queue. Tune in prod.
	cfg.MaxConns = 10
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Ping — if we can't reach the DB at startup, crash now, not on first req.
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) Save(code, longURL string) error {
	// Short timeout: writes should be fast. If the DB is slow, we want to
	// know, not hang the request.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := p.pool.Exec(ctx,
		`INSERT INTO links (code, long_url) VALUES ($1, $2)`,
		code, longURL,
	)
	if err != nil {
		// Postgres SQLSTATE 23505 = unique_violation. Map it to our
		// well-known ErrCodeTaken so handlers can retry on collision.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrCodeTaken
		}
		return err
	}
	return nil
}

func (p *Postgres) Get(code string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var longURL string
	err := p.pool.QueryRow(ctx,
		`SELECT long_url FROM links WHERE code = $1`,
		code,
	).Scan(&longURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return longURL, nil
}
