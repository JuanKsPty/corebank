// Command seed imports the provided dataset.
//
// It runs as a one-shot job in the compose stack, before the API accepts traffic,
// and exits without doing anything if the dataset is already present — so
// restarting the stack is free and re-importing is impossible.
//
// It is a Go program rather than a SQL script because the import has to reach two
// stores: the descriptive half goes to PostgreSQL, and the money has to be opened
// as real double-entry transfers in the ledger. The schema itself is separate, in
// migrations/, and is applied by the API on boot.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/JuanKsPty/corebank/api/internal/config"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/seeder"
	"github.com/JuanKsPty/corebank/api/internal/store"
	"github.com/JuanKsPty/corebank/api/internal/tigerbeetle"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.New(cfg.Log.Level, cfg.Log.Format)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	db, err := store.Open(ctx, cfg.DB.URL, cfg.DB.MaxConns, cfg.DB.ConnectWait, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	// The seeder writes into a schema it does not create. Running the migrations
	// here as well means the job works whether it is scheduled before or after the
	// API — goose is a no-op once the schema is current, so there is no ordering
	// requirement for an operator to get wrong.
	if err := store.Migrate(ctx, db, logger); err != nil {
		return err
	}

	book, err := tigerbeetle.Connect(cfg.TB.ClusterID, cfg.TB.Addresses)
	if err != nil {
		return fmt.Errorf("connecting to the ledger: %w", err)
	}
	defer func() {
		if err := book.Close(); err != nil {
			logger.Error("closing the ledger client failed", "error", err)
		}
	}()

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := book.Ping(pingCtx); err != nil {
		return fmt.Errorf("ledger unreachable at %v: %w", cfg.TB.Addresses, err)
	}

	// The seeder's own bcrypt cost, not the configured one. These are published
	// fixture credentials, and the cost here only decides how long the import
	// takes; registration keeps the configured cost.
	const seedBcryptCost = 10

	report, err := seeder.New(db, book, seedBcryptCost, logger).Run(ctx, cfg.Seed.File)
	if err != nil {
		return err
	}
	if !report.AlreadySeeded && len(report.RewrittenEmails) > 0 {
		// Logged as one line rather than twenty, and repeated in the README: an
		// evaluator trying to log in as one of these people needs to know the
		// address changed.
		logger.Warn("duplicate addresses in the dataset were made unique",
			"count", len(report.RewrittenEmails),
			"rewritten", strings.Join(report.RewrittenEmails, ", "))
	}
	return nil
}
