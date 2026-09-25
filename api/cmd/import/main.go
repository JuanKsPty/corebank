// Command import runs a statement file through internal/imports without the
// web API — for a one-off import run by hand against the deployment's
// database.
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

	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/config"
	"github.com/JuanKsPty/corebank/api/internal/imports"
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
	path := flag.String("file", "", "path to the statement file to import")
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

	svc := imports.NewService(db, categories.NewService(db))
	result, err := svc.Import(ctx, user.ID, filepath.Base(*path), data, nil)
	if err != nil {
		return fmt.Errorf("importing %s: %w", *path, err)
	}
	if result.Unchanged {
		logger.Info("this exact file was already imported; nothing changed")
		return nil
	}
	for _, a := range result.Accounts {
		logger.Info("statement imported", "account", a.DisplayName, "account_id", a.AccountID,
			"period_start", a.PeriodStart.String(), "period_end", a.PeriodEnd.String(),
			"new", a.New, "duplicates", a.Duplicates, "needs_opening", a.NeedsOpening)
		for _, w := range a.Warnings {
			logger.Warn("statement warning", "account_id", a.AccountID, "warning", w)
		}
	}
	return nil
}
