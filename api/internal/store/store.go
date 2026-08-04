// Package store is the PostgreSQL layer: connection pooling, schema migration
// and the repositories the domain services depend on.
//
// It holds identity and metadata only. There is no balance anywhere in this
// package, by design — balances come from the ledger, so the two stores cannot
// drift apart.
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is a pooled connection to PostgreSQL.
type DB struct {
	pool *pgxpool.Pool
}

// Open connects to PostgreSQL, retrying until wait elapses.
//
// The retry loop is not defensive padding: in the compose stack the API can be
// scheduled the instant Postgres reports healthy, and a single refused
// connection at that moment should not turn into a crash-restart cycle the
// evaluator has to watch.
func Open(ctx context.Context, url string, maxConns int32, wait time.Duration, logger *slog.Logger) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("store: parsing DATABASE_URL: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 5 * time.Minute
	// Fail a hung connect attempt fast enough that the retry loop below stays
	// responsive to context cancellation.
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: creating pool: %w", err)
	}

	deadline := time.Now().Add(wait)
	for attempt := 1; ; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return &DB{pool: pool}, nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			pool.Close()
			return nil, fmt.Errorf("store: postgres unreachable after %s: %w", wait, err)
		}
		logger.Warn("postgres not ready, retrying", "attempt", attempt, "error", err)

		select {
		case <-ctx.Done():
			pool.Close()
			return nil, errors.Join(ctx.Err(), err)
		case <-time.After(time.Second):
		}
	}
}

// Querier is the subset of pgx satisfied by both the pool and a transaction, so
// every query in this package runs unchanged inside or outside one.
//
// SendBatch is included for the seeder, which inserts thousands of rows: batching
// pipelines them into a single round trip. COPY would be faster still, but it
// speaks binary and would need the OIDs of this schema's custom types — CITEXT and
// the two enums — registered by hand. Batched inserts keep the server's own type
// inference doing that work, and at this data volume the difference is
// milliseconds.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// Queries is the repository: every SQL statement in the application is a method
// on it. Holding a Querier rather than the pool is what lets the same method be
// called directly or as part of a transaction.
type Queries struct{ q Querier }

// Q returns queries that run outside any transaction, each on its own pooled
// connection.
func (db *DB) Q() *Queries { return &Queries{q: db.pool} }

// InTx runs fn inside a transaction, committing if it returns nil and rolling
// back otherwise — including on a panic, which pgx's BeginFunc handles.
//
// Every multi-statement write in this application goes through here. Registering
// a user, for instance, inserts a user and their first bank account: half of that
// is not a state the system should ever be able to reach.
func (db *DB) InTx(ctx context.Context, fn func(*Queries) error) error {
	return pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		return fn(&Queries{q: tx})
	})
}

// Ping reports whether PostgreSQL is reachable. Used by /healthz.
func (db *DB) Ping(ctx context.Context) error {
	if err := db.pool.Ping(ctx); err != nil {
		return fmt.Errorf("store: ping: %w", err)
	}
	return nil
}

// Close releases every pooled connection.
func (db *DB) Close() { db.pool.Close() }
