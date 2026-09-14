// Command ibkrsync runs an IBKR Flex Query sync for every linked account.
//
// It is a one-shot job, the same shape as cmd/seed: connect, do the work,
// exit. Meant to be invoked on a schedule by something outside the process
// (cron, a Dokploy scheduled job) rather than looping internally, so a stuck
// IBKR request or a misbehaving statement cannot wedge a long-running
// process — the next scheduled invocation is the retry.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/config"
	"github.com/JuanKsPty/corebank/api/internal/investments"
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
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !cfg.IBKR.Enabled() {
		return fmt.Errorf("ibkrsync: IBKR_TOKEN_ENCRYPTION_KEY is not configured; nothing to sync could have been linked")
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

	// Not run here on purpose: this job assumes the API has already brought
	// the schema up to date, the same assumption cmd/tbsmoke makes about the
	// ledger. Migrating from two binaries racing at boot is what goose's own
	// locking guards against, but there is no reason for this job to be the
	// one that does it.

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

	accountsSvc := accounts.NewService(db, book)
	categoriesSvc := categories.NewService(db)
	txSvc := transactions.NewService(db, book, accountsSvc, categoriesSvc, cfg.AI.HoldTTL)
	investmentsSvc := investments.NewService(db, accountsSvc, txSvc, cfg.IBKR.TokenEncryptionKey)

	results, failures := investmentsSvc.SyncAll(ctx)
	for account, result := range results {
		logger.Info("ibkr sync completed", "account_number", account,
			"cash_posted", result.CashMovementsPosted, "trades_recorded", result.TradesRecorded,
			"positions", result.Positions)
	}
	for account, syncErr := range failures {
		logger.Error("ibkr sync failed", "account_number", account, "error", syncErr)
	}

	if len(results) == 0 && len(failures) == 0 {
		logger.Info("no accounts have an IBKR link configured")
	}
	if len(failures) > 0 {
		return fmt.Errorf("ibkrsync: %d of %d accounts failed to sync", len(failures), len(results)+len(failures))
	}
	return nil
}
