// Command api is the corebank backend.
//
// Startup order is deliberate and fails loudly: configuration, then the two
// stores, then the schema, then the ledger's equity account, and only then the
// HTTP listener. Nothing accepts traffic until everything it depends on is
// known to be reachable, so a misconfigured deployment fails at boot with a
// specific message instead of serving 500s.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/auth"
	"github.com/JuanKsPty/corebank/api/internal/bankimport"
	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/chat"
	"github.com/JuanKsPty/corebank/api/internal/config"
	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/investments"
	"github.com/JuanKsPty/corebank/api/internal/ledger"
	"github.com/JuanKsPty/corebank/api/internal/llm"
	"github.com/JuanKsPty/corebank/api/internal/logging"
	"github.com/JuanKsPty/corebank/api/internal/mcpserver"
	"github.com/JuanKsPty/corebank/api/internal/server"
	"github.com/JuanKsPty/corebank/api/internal/store"
	"github.com/JuanKsPty/corebank/api/internal/tigerbeetle"
	"github.com/JuanKsPty/corebank/api/internal/transactions"
)

func main() {
	if err := run(); err != nil {
		// The logger may not exist yet when configuration is what failed, so
		// this last-resort line goes straight to stderr.
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

	logger.Info("starting corebank api",
		"version", server.Version(), "env", cfg.Env, "addr", cfg.HTTP.Addr)

	if cfg.Auth.JWTSecretRandom {
		logger.Warn("JWT_SECRET is not set; using a secret generated for this process only — " +
			"existing sessions will not survive a restart")
	}
	if !cfg.AI.Enabled() {
		logger.Warn("ANTHROPIC_API_KEY is not set; the chat will answer with local rules instead " +
			"of a language model — the MCP tools and the confirmation flow work either way, and " +
			"the interface says which engine is active")
	}

	// SIGTERM is what Docker sends on `compose down`; SIGINT is Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	db, err := store.Open(ctx, cfg.DB.URL, cfg.DB.MaxConns, cfg.DB.ConnectWait, logger)
	if err != nil {
		return err
	}
	defer db.Close()
	logger.Info("postgres connected")

	if err := store.Migrate(ctx, db, logger); err != nil {
		return err
	}
	logger.Info("schema up to date")

	book, err := connectLedger(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		if err := book.Close(); err != nil {
			logger.Error("closing the ledger client failed", "error", err)
		}
	}()

	// Double-entry bookkeeping has no single-sided postings, so the equity
	// counterparty has to exist before the first deposit can be recorded.
	// Creating it is idempotent.
	if err := book.EnsureAccounts(ctx, []ledger.NewAccount{ledger.World()}); err != nil {
		return fmt.Errorf("ensuring the equity account: %w", err)
	}
	logger.Info("ledger ready", "addresses", cfg.TB.Addresses)

	accountsSvc := accounts.NewService(db, book)
	categoriesSvc := categories.NewService(db)

	authSvc, err := auth.NewService(db, accountsSvc, categoriesSvc, cfg.Auth)
	if err != nil {
		return err
	}

	txSvc := transactions.NewService(db, book, accountsSvc, categoriesSvc, cfg.AI.HoldTTL)
	investmentsSvc := investments.NewService(db, accountsSvc, txSvc, cfg.IBKR.TokenEncryptionKey)
	bankImportSvc := bankimport.NewService(db, categoriesSvc, accountsSvc, txSvc)

	// The assistant is built from whichever engine is available, and which one it
	// is stays visible: without an API key the rule-based fallback drives the same
	// MCP tools, and the interface labels it rather than passing it off as an AI.
	provider := selectProvider(db, cfg.AI, logger)
	chatSvc := chat.NewService(db, mcpserver.Deps{
		Accounts:     accountsSvc,
		Transactions: txSvc,
	}, provider, cfg.AI.MaxToolTurns)

	loginLimiter := httpx.NewRateLimiter(cfg.Auth.LoginRateLimit)
	// Its own limiter, not a shared one with login: a six-digit PIN and a
	// password defend against different kinds of guessing, at different rates.
	pinLoginLimiter := httpx.NewRateLimiter(cfg.Auth.PinLoginRateLimit)
	// The chat gets its own budget: a message costs an upstream API call, which is
	// a different order of expense from reading a balance.
	chatLimiter := httpx.NewRateLimiter(chatMessagesPerMinute)

	router := server.NewRouter(server.Deps{
		Config: cfg,
		Logger: logger,
		Health: map[string]httpx.Checker{
			"postgres":    db,
			"tigerbeetle": book,
		},
		Auth:         auth.NewHandler(authSvc, cfg.Env == "production", loginLimiter, pinLoginLimiter),
		Tokens:       authSvc.Tokens(),
		Accounts:     accounts.NewHandler(accountsSvc),
		Transactions: transactions.NewHandler(txSvc),
		Categories:   categories.NewHandler(categoriesSvc),
		Investments:  investments.NewHandler(investmentsSvc),
		BankImport:   bankimport.NewHandler(bankImportSvc),
		Chat:         chat.NewHandler(chatSvc, authSvc, chatLimiter),
	})

	go housekeeping(ctx, authSvc, []*httpx.RateLimiter{loginLimiter, pinLoginLimiter, chatLimiter}, logger)

	// Reconciliation runs from the moment the process starts: its first pass is
	// what recovers movements left unresolved by whatever caused the last restart.
	go transactions.NewSweeper(db, book, cfg.AI.HoldTTL, logger).Run(ctx, time.Minute)

	return server.Run(ctx, cfg.HTTP.Addr, router, cfg.HTTP.ShutdownTimeout, logger)
}

// housekeeping prunes the two structures that would otherwise grow without
// bound: expired refresh tokens in the database, and idle rate-limit buckets in
// memory. Both are cheap, so one modest ticker covers them.
func housekeeping(ctx context.Context, authSvc *auth.Service, limiters []*httpx.RateLimiter, logger *slog.Logger) {
	const every = 15 * time.Minute

	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// A bounded context of its own: this must not be cancelled by the
			// shutdown of an unrelated request, nor hang forever.
			sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			removed, err := authSvc.PruneSessions(sweepCtx)
			cancel()
			if err != nil {
				logger.Error("pruning expired sessions failed", "error", err)
				continue
			}
			var dropped int
			for _, limiter := range limiters {
				dropped += limiter.Sweep()
			}
			logger.Debug("housekeeping", "sessions_pruned", removed,
				"rate_limit_buckets_dropped", dropped)
		}
	}
}

// connectLedger dials TigerBeetle, retrying for the same reason Postgres does:
// in the compose stack the replica may report healthy a moment before it
// accepts a client, and one refused connection should not become a restart loop.
func connectLedger(ctx context.Context, cfg config.Config, logger *slog.Logger) (*tigerbeetle.Adapter, error) {
	deadline := time.Now().Add(cfg.TB.ConnectWait)

	for attempt := 1; ; attempt++ {
		adapter, err := tigerbeetle.Connect(cfg.TB.ClusterID, cfg.TB.Addresses)
		if err == nil {
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = adapter.Ping(pingCtx)
			cancel()
			if err == nil {
				return adapter, nil
			}
			_ = adapter.Close()
		}

		if ctx.Err() != nil {
			return nil, errors.Join(ctx.Err(), err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("tigerbeetle unreachable at %v after %s: %w",
				cfg.TB.Addresses, cfg.TB.ConnectWait, err)
		}
		logger.Warn("tigerbeetle not ready, retrying", "attempt", attempt, "error", err)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// chatMessagesPerMinute is the per-address budget for chat messages.
//
// Six, not twenty. Twenty was chosen when a message cost nothing but database
// round trips; with a real model behind it, this number is the rate at which a
// stranger can spend the deployment's money, and each message can drive several
// API calls. Six per minute is faster than anybody types and slow enough that the
// spend ceiling is reached by usage rather than by a script.
const chatMessagesPerMinute = 6

// selectProvider picks the engine behind the assistant.
//
// A missing API key must never stop the process from booting or make the chat
// return an error: the evaluator's first run has no credentials, and an
// application where one feature crashes the page is worse than one where it is
// honestly labelled. So the fallback drives the same MCP tools through the same
// loop, and the interface says which engine answered.
//
// When a key *is* present, the model never gets called directly. It goes behind the
// spend ceilings, because this deployment is public and the key on it is somebody's
// actual money: registration is open and the test credentials are in the README, so
// without a ceiling the budget is whatever a stranger decides to spend. The wrapper
// also means the assistant degrades to rules when the money or the key runs out,
// rather than telling customers to "try again in a moment" forever.
func selectProvider(db *store.DB, cfg config.AIConfig, logger *slog.Logger) llm.Provider {
	fallback := chat.NewFallback()

	if !cfg.Enabled() {
		return fallback
	}

	provider, err := llm.NewAnthropic(cfg.APIKey, cfg.Model)
	if err != nil {
		// A key that is present but unusable — empty after trimming, say. Falling
		// back keeps the application working and says why.
		logger.Error("the AI provider could not be built; falling back to local rules", "error", err)
		return fallback
	}

	caps := chat.Caps{
		Total:     cfg.BudgetMicros,
		Daily:     cfg.DailyBudgetMicros,
		UserDaily: cfg.UserDailyBudgetMicros,
	}
	if caps.Total == 0 {
		// A key with no allowance to spend it. Worth saying out loud, because the
		// symptom — an assistant that answers from rules despite a configured key —
		// otherwise looks like the key being broken.
		logger.Warn("an API key is configured but AI_BUDGET_USD is zero; the assistant will answer from rules")
	}
	logger.Info("AI assistant enabled",
		"model", cfg.Model,
		"budget_micros", caps.Total,
		"daily_micros", caps.Daily,
		"user_daily_micros", caps.UserDaily,
		"max_tool_turns", cfg.MaxToolTurns)

	return chat.NewBudgeted(provider, fallback, chat.NewKeeper(db), caps)
}
