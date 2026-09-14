// Command import runs a bank statement file through internal/bankimport
// without going through the web API — for testing the three known parsers
// against a real file, or for a one-off import Juan runs by hand.
//
// It does not touch the ledger or TigerBeetle at all: bank import is
// Postgres-only by design (see internal/bankimport's package doc), so this
// only needs a database connection, unlike cmd/seed and cmd/ibkrsync.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/JuanKsPty/corebank/api/internal/bankimport"
	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/config"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	email := flag.String("email", "", "email of the corebank user to import into")
	path := flag.String("file", "", "path to the bank statement file to import")
	flag.Parse()

	if *email == "" || *path == "" {
		return fmt.Errorf("usage: import -email <email> -file <path>")
	}

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

	user, err := db.Q().UserByEmail(ctx, strings.ToLower(strings.TrimSpace(*email)))
	if err != nil {
		return fmt.Errorf("looking up user %q: %w", *email, err)
	}

	data, err := os.ReadFile(*path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", *path, err)
	}

	svc := bankimport.NewService(db, categories.NewService(db))
	result, err := svc.Import(ctx, user.ID, filepath.Base(*path), data)
	if err != nil {
		return fmt.Errorf("importing %s: %w", *path, err)
	}

	logger.Info("import completed",
		"external_account_id", result.ImportedAccountID,
		"format", result.Format,
		"total_rows", result.TotalRows,
		"imported", result.Imported,
		"skipped_duplicates", result.SkippedDuplicates)
	return nil
}
