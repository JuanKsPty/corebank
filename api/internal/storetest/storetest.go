// Package storetest gives a test its own freshly migrated PostgreSQL database.
//
// Tests that need a real database call New. It reads TEST_DATABASE_URL, a
// connection string to any database on a server the test may create databases
// on, creates a throwaway database there, runs every migration against it and
// drops it when the test ends. Each call gets its own database, so tests can run
// in parallel without seeing each other's rows.
//
// Without TEST_DATABASE_URL the test is skipped, so `go test ./...` still runs on
// a laptop with no Postgres. In CI (CI=true) a missing or unreachable database is
// a failure instead: a suite whose database tests silently skip is how a
// money path ends up with zero integration tests while CI stays green.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/JuanKsPty/corebank/api/internal/store"
)

// EnvURL names the variable holding the server connection string.
const EnvURL = "TEST_DATABASE_URL"

// New returns a migrated database that is dropped when t ends.
func New(t testing.TB) *store.DB {
	t.Helper()

	base := os.Getenv(EnvURL)
	if base == "" {
		if os.Getenv("CI") == "true" {
			t.Fatalf("storetest: %s is not set; CI must run the database tests, not skip them", EnvURL)
		}
		t.Skipf("storetest: %s is not set", EnvURL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("storetest: connecting to %s: %v", EnvURL, err)
	}
	defer admin.Close(ctx)

	name := databaseName(t)
	// Identifiers cannot be bound as parameters; name is built from
	// [a-z0-9_] only, so quoting it is enough.
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		t.Fatalf("storetest: creating database %s: %v", name, err)
	}
	t.Cleanup(func() { dropDatabase(t, base, name) })

	target, err := withDatabase(base, name)
	if err != nil {
		t.Fatalf("storetest: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := store.Open(ctx, target, 4, 5*time.Second, logger)
	if err != nil {
		t.Fatalf("storetest: opening %s: %v", name, err)
	}
	t.Cleanup(db.Close)

	if err := store.Migrate(ctx, db, logger); err != nil {
		t.Fatalf("storetest: migrating %s: %v", name, err)
	}
	return db
}

var unsafeChars = regexp.MustCompile(`[^a-z0-9_]+`)

// databaseName is readable in a psql listing (it starts with the test's name)
// and unique (it ends with random bytes), within Postgres's 63-byte limit.
func databaseName(t testing.TB) string {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("storetest: random suffix: %v", err)
	}
	slug := unsafeChars.ReplaceAllString(strings.ToLower(t.Name()), "_")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	return "test_" + slug + "_" + hex.EncodeToString(suffix[:])
}

// withDatabase swaps the database in a connection URL, keeping everything
// else (credentials, host, sslmode).
func withDatabase(raw, name string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}

func dropDatabase(t testing.TB, base, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Logf("storetest: could not drop %s: %v", name, err)
		return
	}
	defer admin.Close(ctx)
	// FORCE ends any connection a test leaked, so cleanup never hangs on it.
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`); err != nil {
		t.Logf("storetest: could not drop %s: %v", name, err)
	}
}

// Exec runs raw SQL against db, for a test asserting what the schema itself
// allows. It returns the error instead of failing, since refusals are usually
// the point.
func Exec(t testing.TB, db *store.DB, sql string, args ...any) error {
	t.Helper()
	return db.Exec(context.Background(), sql, args...)
}
