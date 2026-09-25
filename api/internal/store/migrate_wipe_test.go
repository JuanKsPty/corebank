package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/JuanKsPty/corebank/api/migrations"
)

// scratchDB creates an empty database for one test. It lives here rather than
// in storetest because storetest imports this package.
func scratchDB(t *testing.T) *DB {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		if os.Getenv("CI") == "true" {
			t.Fatal("TEST_DATABASE_URL is not set")
		}
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	var b [4]byte
	rand.Read(b[:])
	name := "test_wipe_" + hex.EncodeToString(b[:])
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), base)
		if err == nil {
			c.Exec(context.Background(), `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`)
			c.Close(context.Background())
		}
	})
	u, _ := url.Parse(base)
	u.Path = "/" + name
	db, err := Open(ctx, u.String(), 4, 5*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

// migrateTo brings db to exactly version v with goose directly, bypassing the
// guard, to set up a database as it was before the wipe.
func migrateTo(t *testing.T, db *DB, v int64) {
	t.Helper()
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDBFromPool(db.pool)
	defer sqlDB.Close()
	if err := goose.UpToContext(context.Background(), sqlDB, ".", v); err != nil {
		t.Fatal(err)
	}
}

func exec(t *testing.T, db *DB, sql string, args ...any) {
	t.Helper()
	if _, err := db.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func count(t *testing.T, db *DB, sql string) int {
	t.Helper()
	var n int
	if err := db.pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func TestTheWipeMigrationKeepsWhatItPromisesAndDropsTheRest(t *testing.T) {
	db := scratchDB(t)
	migrateTo(t, db, 10)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// A database as production has it: a user, an investment account with an
	// IBKR link, a bank account with history, categories and rules that were
	// re-seeded twice.
	exec(t, db, `INSERT INTO users (id, email, password_hash, full_name) VALUES
		('00000000-0000-0000-0000-000000000001', 'a@example.com', 'x', 'A')`)
	exec(t, db, `INSERT INTO accounts (id, user_id, account_number, ledger_id, account_type) VALUES
		('00000000-0000-0000-0000-00000000000a', '00000000-0000-0000-0000-000000000001', '4001-0000-0000-0001', 4001000000000001, 'investment'),
		('00000000-0000-0000-0000-00000000000b', '00000000-0000-0000-0000-000000000001', '4001-0000-0000-0002', 4001000000000002, 'checking')`)
	exec(t, db, `INSERT INTO ibkr_links (id, account_id, ibkr_account_id, flex_query_id, flex_token_ciphertext, last_sync_status)
		VALUES ('00000000-0000-0000-0000-0000000000c1', '00000000-0000-0000-0000-00000000000a', 'U1234567', '99', '\x01', 'ok')`)
	exec(t, db, `INSERT INTO transactions (id, kind, status, amount_cents, to_account, occurred_at)
		VALUES ('00000000-0000-0000-0000-0000000000d1', 'deposit', 'completed', 100, '4001-0000-0000-0002', now())`)
	exec(t, db, `INSERT INTO categories (id, user_id, name, kind) VALUES
		('00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-000000000001', 'Comida', 'expense')`)
	exec(t, db, `INSERT INTO category_rules (id, user_id, match_text, category_id, created_at) VALUES
		('00000000-0000-0000-0000-0000000000f1', '00000000-0000-0000-0000-000000000001', 'cafe', '00000000-0000-0000-0000-0000000000e1', now() - interval '1 day'),
		('00000000-0000-0000-0000-0000000000f2', '00000000-0000-0000-0000-000000000001', ' CAFE ', '00000000-0000-0000-0000-0000000000e1', now())`)

	// Without the confirmation the API refuses to boot rather than wipe.
	os.Unsetenv(ConfirmWipeEnv)
	if err := Migrate(context.Background(), db, logger); !errors.Is(err, ErrWipeNotConfirmed) {
		t.Fatalf("Migrate without confirmation = %v, want ErrWipeNotConfirmed", err)
	}
	if n := count(t, db, `SELECT count(*) FROM transactions`); n != 1 {
		t.Fatalf("the refused migration touched data: %d transactions left", n)
	}

	t.Setenv(ConfirmWipeEnv, "00011")
	if err := Migrate(context.Background(), db, logger); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if n := count(t, db, `SELECT count(*) FROM users`); n != 1 {
		t.Errorf("users = %d, want 1", n)
	}
	if n := count(t, db, `SELECT count(*) FROM category_rules`); n != 1 {
		t.Errorf("category_rules = %d, want 1 after deduplication", n)
	}
	// The IBKR link survives, pointing at a brokerage account with the same id.
	if n := count(t, db, `SELECT count(*) FROM ibkr_links l JOIN accounts a ON a.id = l.account_id
		WHERE a.type = 'brokerage' AND a.external_number = 'U1234567' AND l.last_sync_status = 'never'`); n != 1 {
		t.Errorf("the IBKR link did not survive with its brokerage account")
	}
	// Only the brokerage account exists; the old bank account and its history are gone.
	if n := count(t, db, `SELECT count(*) FROM accounts`); n != 1 {
		t.Errorf("accounts = %d, want only the recreated brokerage account", n)
	}
	if n := count(t, db, `SELECT count(*) FROM pg_tables WHERE tablename IN ('transactions', 'external_transactions', 'seed_state', 'idempotency_keys')`); n != 0 {
		t.Errorf("%d old money tables still exist", n)
	}

	// And a second boot is a no-op, with or without the variable.
	os.Unsetenv(ConfirmWipeEnv)
	if err := Migrate(context.Background(), db, logger); err != nil {
		t.Errorf("second Migrate: %v", err)
	}
}

func TestAFreshDatabaseMigratesWithoutConfirmation(t *testing.T) {
	db := scratchDB(t)
	os.Unsetenv(ConfirmWipeEnv)
	if err := Migrate(context.Background(), db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate on an empty database: %v", err)
	}
}
