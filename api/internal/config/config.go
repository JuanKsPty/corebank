// Package config turns the process environment into a validated struct.
//
// Two rules shape it. First, every setting has a working default, so a fresh
// clone boots with `cp .env.example .env && docker compose up` and nothing
// else. Second, loading either succeeds completely or reports *all* the
// problems at once — discovering a second bad setting only after fixing the
// first is a miserable way to start a container.
package config

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/JuanKsPty/corebank/api/internal/money"
)

// Config is the whole configuration of the API process.
type Config struct {
	Env  string // "development" or "production"; affects log format and error detail
	HTTP HTTPConfig
	DB   DBConfig
	TB   TigerBeetleConfig
	Auth AuthConfig
	AI   AIConfig
	Log  LogConfig
	Seed SeedConfig
}

type HTTPConfig struct {
	Addr string
	// ShutdownTimeout bounds how long in-flight requests get to finish after a
	// SIGTERM before the process exits anyway.
	ShutdownTimeout time.Duration
	// CORSOrigins are the browser origins allowed to call the API. In the
	// container image the frontend is served by nginx on the same origin, so
	// this is empty in production and only used for `npm run dev`.
	CORSOrigins []string
}

type DBConfig struct {
	URL         string
	MaxConns    int32
	ConnectWait time.Duration
}

type TigerBeetleConfig struct {
	ClusterID   uint64
	Addresses   []string
	ConnectWait time.Duration
}

type AuthConfig struct {
	// JWTSecret signs access tokens. See Load for what happens when it is unset.
	JWTSecret       []byte
	JWTSecretRandom bool
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	BcryptCost      int
	// LoginRateLimit is the number of login attempts allowed per IP per minute.
	LoginRateLimit int
}

type AIConfig struct {
	APIKey string
	Model  string
	// HoldTTL is how long a movement proposed by the assistant keeps its funds
	// reserved while waiting for the user to confirm. When it elapses the
	// ledger releases the reservation on its own — no cleanup job involved.
	HoldTTL time.Duration
	// MaxToolTurns bounds the agentic loop so a confused model cannot spin.
	MaxToolTurns int

	// The three spend ceilings, in micro-dollars. Micro rather than cents because
	// one call can cost a fraction of a cent, and a ceiling that cannot represent
	// the thing it counts is not a ceiling.
	//
	// They exist because this deployment is public, registration is open and the
	// test credentials are published: without them, the budget is whatever a
	// stranger decides to spend. Zero disables the assistant's use of the API
	// entirely, which is a supported state — the rule-based engine answers.
	//
	// BudgetMicros is the lifetime ceiling and the one that matters.
	BudgetMicros int64
	// DailyBudgetMicros keeps one bad day from consuming the lifetime budget.
	DailyBudgetMicros int64
	// UserDailyBudgetMicros keeps one account from consuming everyone else's share.
	UserDailyBudgetMicros int64
}

// Enabled reports whether the chat assistant can reach a model. When false the
// rest of the application is unaffected and the chat endpoint says so plainly.
func (c AIConfig) Enabled() bool { return c.APIKey != "" }

type LogConfig struct {
	Level  string // debug | info | warn | error
	Format string // json | text
}

type SeedConfig struct {
	// File is the dataset the seeder imports, relative to the working directory.
	File string
}

// Load reads the environment and validates it.
func Load() (Config, error) {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	env := oneOf("ENV", "development", "development", "production")
	if env == "" {
		fail("ENV must be development or production, got %q", os.Getenv("ENV"))
		env = "development"
	}

	cfg := Config{
		Env: env,
		HTTP: HTTPConfig{
			Addr:            str("HTTP_ADDR", ":8080"),
			ShutdownTimeout: duration("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second, fail),
			CORSOrigins:     list("CORS_ORIGINS", "http://localhost:5173"),
		},
		DB: DBConfig{
			URL:         str("DATABASE_URL", "postgres://corebank:corebank@localhost:5432/corebank?sslmode=disable"),
			MaxConns:    int32(number("DB_MAX_CONNS", 10, fail)),
			ConnectWait: duration("DB_CONNECT_WAIT", 30*time.Second, fail),
		},
		TB: TigerBeetleConfig{
			ClusterID:   uint64(number("TB_CLUSTER_ID", 0, fail)),
			Addresses:   list("TB_ADDRESSES", "127.0.0.1:3001"),
			ConnectWait: duration("TB_CONNECT_WAIT", 30*time.Second, fail),
		},
		Auth: AuthConfig{
			AccessTTL:      duration("ACCESS_TOKEN_TTL", 15*time.Minute, fail),
			RefreshTTL:     duration("REFRESH_TOKEN_TTL", 720*time.Hour, fail),
			BcryptCost:     number("BCRYPT_COST", 10, fail),
			LoginRateLimit: number("LOGIN_RATE_LIMIT", 10, fail),
		},
		AI: AIConfig{
			APIKey:  str("ANTHROPIC_API_KEY", ""),
			Model:   str("ANTHROPIC_MODEL", "claude-sonnet-5"),
			HoldTTL: duration("CONFIRMATION_TTL", 2*time.Minute, fail),
			// Four, not eight. A banking question needs one or two rounds of
			// tools; the rest of the allowance only ever bought a confused model
			// more chances to keep asking, and each round re-sends the whole
			// conversation.
			MaxToolTurns:          number("AI_MAX_TOOL_TURNS", 4, fail),
			BudgetMicros:          dollars("AI_BUDGET_USD", "4.00", fail),
			DailyBudgetMicros:     dollars("AI_DAILY_BUDGET_USD", "1.00", fail),
			UserDailyBudgetMicros: dollars("AI_USER_DAILY_BUDGET_USD", "0.30", fail),
		},
		Log: LogConfig{
			Level:  oneOf("LOG_LEVEL", "info", "debug", "info", "warn", "error"),
			Format: oneOf("LOG_FORMAT", defaultLogFormat(env), "json", "text"),
		},
		Seed: SeedConfig{
			File: str("SEED_FILE", "seed/data/hnl-seed.json"),
		},
	}

	// An unset signing secret must not stop the process from booting — the
	// evaluator's first run has no .env of its own — but a hardcoded fallback
	// committed to a public repository would be worse than no secret at all.
	// So one is generated per process: everything works, and the only cost is
	// that sessions do not survive a restart. Load's caller warns about it.
	if secret := os.Getenv("JWT_SECRET"); secret != "" {
		if len(secret) < 32 {
			fail("JWT_SECRET must be at least 32 characters, got %d", len(secret))
		}
		cfg.Auth.JWTSecret = []byte(secret)
	} else {
		secret, err := randomSecret(32)
		if err != nil {
			fail("generating an ephemeral JWT secret: %w", err)
		}
		cfg.Auth.JWTSecret = secret
		cfg.Auth.JWTSecretRandom = true
	}

	if cfg.Log.Level == "" {
		fail("LOG_LEVEL must be one of debug, info, warn, error; got %q", os.Getenv("LOG_LEVEL"))
	}
	if cfg.Log.Format == "" {
		fail("LOG_FORMAT must be json or text, got %q", os.Getenv("LOG_FORMAT"))
	}
	if cfg.DB.MaxConns < 1 {
		fail("DB_MAX_CONNS must be at least 1, got %d", cfg.DB.MaxConns)
	}
	// bcrypt itself rejects anything outside 4..31; below 10 is too cheap to be
	// worth shipping, and the ceiling keeps a typo from making login take
	// minutes instead of milliseconds.
	if cfg.Auth.BcryptCost < 10 || cfg.Auth.BcryptCost > 15 {
		fail("BCRYPT_COST must be between 10 and 15, got %d", cfg.Auth.BcryptCost)
	}
	if len(cfg.TB.Addresses) == 0 {
		fail("TB_ADDRESSES must not be empty")
	}
	if cfg.AI.MaxToolTurns < 1 {
		fail("AI_MAX_TOOL_TURNS must be at least 1, got %d", cfg.AI.MaxToolTurns)
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return cfg, nil
}

func randomSecret(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func defaultLogFormat(env string) string {
	if env == "production" {
		return "json"
	}
	return "text"
}

func str(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// oneOf returns the value only if it is in allowed, and "" otherwise so the
// caller can report a usable error naming the variable.
func oneOf(key, def string, allowed ...string) string {
	v := strings.ToLower(str(key, def))
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return ""
}

func list(key, def string) []string {
	var out []string
	for _, part := range strings.Split(str(key, def), ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func number(key string, def int, fail func(string, ...any)) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		fail("%s must be an integer, got %q", key, raw)
		return def
	}
	return n
}

// microsPerCent scales a cent into the micro-dollars the budget counts in.
const microsPerCent = 10_000

// dollars reads a spend ceiling written the way a person writes money ("4.00")
// and returns it in micro-dollars.
//
// It goes through money.Parse rather than strconv.ParseFloat for the same reason
// every other amount in this system does: parsing "0.30" as a float and scaling it
// gives 299999.99999999994 micro-dollars, and a ceiling that is a hair under what
// was asked for is a ceiling nobody can reason about. Parse works on the decimal
// text and refuses anything more precise than a cent.
func dollars(key, def string, fail func(string, ...any)) int64 {
	raw := str(key, def)

	cents, err := money.Parse(raw)
	if err != nil {
		fail("%s must be an amount in dollars such as 4.00, got %q: %v", key, raw, err)
		return 0
	}
	if cents < 0 {
		fail("%s must not be negative, got %q", key, raw)
		return 0
	}
	return int64(cents) * microsPerCent
}

func duration(key string, def time.Duration, fail func(string, ...any)) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		fail("%s must be a duration such as 15m or 30s, got %q", key, raw)
		return def
	}
	if d <= 0 {
		fail("%s must be positive, got %q", key, raw)
		return def
	}
	return d
}
