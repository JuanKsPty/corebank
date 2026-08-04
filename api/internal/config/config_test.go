package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// The promise this test pins down: a fresh clone with no environment at all
	// still produces a bootable configuration.
	clearEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with an empty environment: %v", err)
	}

	if cfg.HTTP.Addr != ":8080" {
		t.Errorf("HTTP.Addr = %q, want :8080", cfg.HTTP.Addr)
	}
	if cfg.Auth.AccessTTL != 15*time.Minute {
		t.Errorf("Auth.AccessTTL = %v, want 15m", cfg.Auth.AccessTTL)
	}
	if cfg.Env != "development" || cfg.Log.Format != "text" {
		t.Errorf("env %q with log format %q, want development/text", cfg.Env, cfg.Log.Format)
	}
	if cfg.AI.Enabled() {
		t.Error("AI reports itself enabled without an API key")
	}
}

func TestJWTSecretIsGeneratedWhenUnset(t *testing.T) {
	// Booting without a secret must work, and the generated one must be random —
	// a fixed fallback compiled into a public binary would be worse than none.
	clearEnv(t)

	first, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	second, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !first.Auth.JWTSecretRandom {
		t.Error("JWTSecretRandom is false, so the caller cannot warn about it")
	}
	if len(first.Auth.JWTSecret) < 32 {
		t.Errorf("generated secret is %d bytes, want at least 32", len(first.Auth.JWTSecret))
	}
	if string(first.Auth.JWTSecret) == string(second.Auth.JWTSecret) {
		t.Error("two loads produced the same secret, so it is not random")
	}
}

func TestJWTSecretFromEnv(t *testing.T) {
	clearEnv(t)
	secret := strings.Repeat("k", 32)
	t.Setenv("JWT_SECRET", secret)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(cfg.Auth.JWTSecret) != secret {
		t.Error("the configured secret was not used")
	}
	if cfg.Auth.JWTSecretRandom {
		t.Error("JWTSecretRandom is true even though a secret was configured")
	}
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	// Fixing one bad variable only to be told about the next one is a miserable
	// way to start a container, so Load must not stop at the first failure.
	clearEnv(t)
	t.Setenv("JWT_SECRET", "too-short")
	t.Setenv("BCRYPT_COST", "3")
	t.Setenv("ACCESS_TOKEN_TTL", "fifteen minutes")
	t.Setenv("DB_MAX_CONNS", "not-a-number")

	_, err := Load()
	if err == nil {
		t.Fatal("Load accepted four invalid values")
	}

	msg := err.Error()
	for _, want := range []string{"JWT_SECRET", "BCRYPT_COST", "ACCESS_TOKEN_TTL", "DB_MAX_CONNS"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not mention %s:\n%s", want, msg)
		}
	}
}

func TestLoadRejects(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"ENV", "staging"},
		{"LOG_LEVEL", "verbose"},
		{"LOG_FORMAT", "logfmt"},
		{"BCRYPT_COST", "31"}, // valid for bcrypt, but login would take minutes
		{"BCRYPT_COST", "4"},  // valid for bcrypt, but far too cheap to ship
		{"DB_MAX_CONNS", "0"},
		{"HTTP_SHUTDOWN_TIMEOUT", "-5s"},
		{"AI_MAX_TOOL_TURNS", "0"},
		{"TB_ADDRESSES", ","},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(tc.key, tc.value)

			if _, err := Load(); err == nil {
				t.Errorf("Load accepted %s=%q", tc.key, tc.value)
			}
		})
	}
}

// clearEnv unsets every variable Load reads, so a test does not depend on the
// developer's shell or on .env being present. t.Setenv restores them after.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"ENV", "HTTP_ADDR", "HTTP_SHUTDOWN_TIMEOUT", "CORS_ORIGINS",
		"DATABASE_URL", "DB_MAX_CONNS", "DB_CONNECT_WAIT",
		"TB_CLUSTER_ID", "TB_ADDRESSES", "TB_CONNECT_WAIT",
		"JWT_SECRET", "ACCESS_TOKEN_TTL", "REFRESH_TOKEN_TTL", "BCRYPT_COST", "LOGIN_RATE_LIMIT",
		"ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "CONFIRMATION_TTL", "AI_MAX_TOOL_TURNS",
		"LOG_LEVEL", "LOG_FORMAT", "SEED_FILE",
	} {
		t.Setenv(key, "")
	}
}
