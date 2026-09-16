// Command import runs a bank statement file through internal/bankimport
// without going through the web API — for testing the known parsers against
// a real file, or for a one-off import Juan runs by hand.
//
// A card statement stays Postgres-only, but a bank-account statement now
// posts real ledger movements (see internal/bankimport's package doc), so
// this needs a TigerBeetle connection too, exactly like cmd/ibkrsync.
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
	"time"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/bankimport"
	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/config"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/store"
	"github.com/JuanKsPty/corebank/api/internal/tigerbeetle"
	"github.com/JuanKsPty/corebank/api/internal/transactions"
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
	account := flag.String("account", "", "number of the existing corebank account to link a bank-account statement to (optional; a new account is opened automatically when omitted; ignored for a card statement)")
	flag.Parse()

	if *email == "" || *path == "" {
		return fmt.Errorf("usage: import -email <email> -file <path> [-account <number>]")
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

	user, err := db.Q().UserByEmail(ctx, strings.ToLower(strings.TrimSpace(*email)))
	if err != nil {
		return fmt.Errorf("looking up user %q: %w", *email, err)
	}

	data, err := os.ReadFile(*path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", *path, err)
	}

	accountsSvc := accounts.NewService(db, book)
	categoriesSvc := categories.NewService(db)
	txSvc := transactions.NewService(db, book, accountsSvc, categoriesSvc, cfg.AI.HoldTTL)

	svc := bankimport.NewService(db, categoriesSvc, accountsSvc, txSvc)
	result, err := svc.Import(ctx, user.ID, filepath.Base(*path), data, *account)
	if err != nil {
		return fmt.Errorf("importing %s: %w", *path, err)
	}

	logger.Info("import completed",
		"external_account_id", result.ImportedAccountID,
		"format", result.Format,
		"total_rows", result.TotalRows,
		"imported", result.Imported,
		"skipped_duplicates", result.SkippedDuplicates,
		"is_card", result.IsCard,
		"linked_account_number", result.LinkedAccountNumber)
	return nil
}
