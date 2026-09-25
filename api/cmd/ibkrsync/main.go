// Command ibkrsync syncs every linked IBKR account through the Flex Web
// Service.
//
// A one-shot job — connect, sync, exit — meant to be run on a schedule by
// something outside the process (cron, a Dokploy scheduled job), so a stuck
// IBKR request cannot wedge a long-running process: the next run is the retry.
// Run it in the morning, after IBKR's overnight processing has published the
// previous day.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/JuanKsPty/corebank/api/internal/config"
	"github.com/JuanKsPty/corebank/api/internal/investments"
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
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !cfg.IBKR.Enabled() {
		return fmt.Errorf("ibkrsync: IBKR_TOKEN_ENCRYPTION_KEY is not configured; nothing could have been linked")
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

	// The schema is the API's to migrate; this job assumes it is current.
	svc := investments.NewService(db, cfg.IBKR.TokenEncryptionKey)
	results, failures := svc.SyncAll(ctx)

	// Sync logs each account's counts and period; what a scheduled run's log
	// needs on top is anything that looked successful but was not.
	for account, result := range results {
		for _, warning := range result.Warnings {
			logger.Warn("ibkr sync warning", "account_id", account, "warning", warning)
		}
	}
	for account, syncErr := range failures {
		logger.Error("ibkr sync failed", "account_id", account, "error", syncErr)
	}
	if len(results) == 0 && len(failures) == 0 {
		logger.Info("no accounts have an IBKR link configured")
	}
	if len(failures) > 0 {
		return fmt.Errorf("ibkrsync: %d of %d accounts failed to sync", len(failures), len(results)+len(failures))
	}
	return nil
}
