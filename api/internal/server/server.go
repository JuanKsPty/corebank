// Package server assembles the HTTP surface: the middleware stack, the routing
// table, and the run/shutdown lifecycle.
//
// Nothing here knows how a feature works. It receives already-built services
// and mounts them, which keeps the routing table readable as a table of
// contents for the API and keeps the wiring in one place instead of spread
// across handler files.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/JuanKsPty/corebank/api/internal/accounts"
	"github.com/JuanKsPty/corebank/api/internal/auth"
	"github.com/JuanKsPty/corebank/api/internal/categories"
	"github.com/JuanKsPty/corebank/api/internal/chat"
	"github.com/JuanKsPty/corebank/api/internal/config"
	"github.com/JuanKsPty/corebank/api/internal/httpx"
	"github.com/JuanKsPty/corebank/api/internal/imports"
	"github.com/JuanKsPty/corebank/api/internal/investments"
	"github.com/JuanKsPty/corebank/api/internal/movements"
	"github.com/JuanKsPty/corebank/api/internal/proposals"
	"github.com/JuanKsPty/corebank/api/internal/reports"
	"github.com/JuanKsPty/corebank/api/internal/rules"
	"github.com/JuanKsPty/corebank/api/internal/transfers"
)

// Deps are the collaborators the routing table needs. It grows as features land;
// keeping it an explicit struct rather than a service locator means a missing
// dependency is a compile error.
type Deps struct {
	Config config.Config
	Logger *slog.Logger
	// Health is the set of backing stores /healthz probes, keyed by the name
	// that appears in the response.
	Health map[string]httpx.Checker

	Auth        *auth.Handler
	Tokens      *auth.TokenIssuer
	Accounts    *accounts.Handler
	Movements   *movements.Handler
	Categories  *categories.Handler
	Investments *investments.Handler
	Imports     *imports.Handler
	Reports     *reports.Handler
	Transfers   *transfers.Handler
	Rules       *rules.Handler
	Proposals   *proposals.Handler
	Chat        *chat.Handler
}

// NewRouter builds the handler for the whole API.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	// Order matters. RequestID first so every later layer — including the
	// panic handler — can name the request; Recoverer above the logger so a
	// panic is still recorded as a 500 in the access log.
	r.Use(httpx.RequestID)
	r.Use(httpx.RequestLogger(d.Logger))
	r.Use(httpx.Recoverer)
	r.Use(httpx.SecurityHeaders)
	r.Use(httpx.CORS(d.Config.HTTP.CORSOrigins))

	// Outside /api and unauthenticated on purpose: compose's healthcheck and
	// any orchestrator have no credentials.
	r.Get("/healthz", httpx.Health(Version(), d.Health))

	r.Route("/api", func(r chi.Router) {
		// Public: issuing and ending sessions. Rate limiting lives inside the
		// auth router, which knows which of its endpoints are worth guessing at.
		r.Mount("/auth", d.Auth.Routes())

		// Everything past here requires a verified access token. Grouping it
		// under one middleware, rather than applying it per handler, is what
		// makes forgetting it impossible: a route added inside this block is
		// authenticated whether or not its author thought about it.
		r.Group(func(r chi.Router) {
			r.Use(auth.Require(d.Tokens))

			r.Get("/me", d.Auth.Me)
			r.Mount("/accounts", d.Accounts.Routes(d.Movements.AccountEntries))
			r.Mount("/entries", d.Movements.Routes())
			r.Mount("/categories", d.Categories.Routes())
			r.Mount("/investments", d.Investments.Routes())
			r.Mount("/imports", d.Imports.Routes())
			r.Mount("/reports", d.Reports.Routes())
			r.Mount("/transfers", d.Transfers.Routes())
			r.Mount("/category-rules", d.Rules.Routes())
			r.Mount("/proposals", d.Proposals.Routes())
			r.Mount("/chat", d.Chat.Routes())
		})

		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			httpx.Fail(w, r, httpx.NotFound("not_found", "El recurso solicitado no existe."))
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			httpx.Fail(w, r, &httpx.Error{
				Status:  http.StatusMethodNotAllowed,
				Code:    "method_not_allowed",
				Message: "El método no está permitido en este recurso.",
			})
		})
	})

	return r
}

// version is overridable at build time with
// -ldflags "-X .../internal/server.version=v1.2.3".
var version = ""

// Version reports the running build. It falls back to the VCS revision the Go
// toolchain stamps into the binary, so an image built without ldflags still
// reports something traceable instead of "unknown".
func Version() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			if len(s.Value) > 12 {
				return s.Value[:12]
			}
			return s.Value
		}
	}
	return "dev"
}

// Run serves h on addr until ctx is cancelled, then drains in-flight requests.
//
// The two-stage shutdown is what makes a redeploy invisible to a customer
// mid-transaction: Shutdown stops accepting connections and waits for the
// handlers already running, and only a handler that overruns the timeout is cut
// off. Close is the last resort so the process cannot hang forever.
func Run(ctx context.Context, addr string, h http.Handler, shutdownTimeout time.Duration, logger *slog.Logger) error {
	srv := &http.Server{
		Handler: h,
		Addr:    addr,
		// Generous but finite: a slow client must not be able to hold a
		// connection open indefinitely. WriteTimeout is deliberately absent —
		// it would cap the chat's SSE stream, whose whole job is to stay open.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.With("source", "http").Handler(), slog.LevelWarn),
	}
	// Request contexts intentionally do not inherit ctx: cancelling it means
	// "start shutting down", and a movement already in flight must be allowed
	// to finish rather than be aborted between the ledger and the database.

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("server: listen: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down", "timeout", shutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed, closing connections", "error", err)
		_ = srv.Close()
		return fmt.Errorf("server: shutdown: %w", err)
	}
	logger.Info("shutdown complete")
	return <-errCh
}
