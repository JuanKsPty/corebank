package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/JuanKsPty/corebank/api/migrations"
)

// Migrate brings the schema up to date from the migrations embedded in the
// binary.
//
// It runs on every API start rather than as a separate step, so there is no
// order for an operator to get wrong and no way for the running code and the
// schema to disagree. goose records applied versions in its own table, so
// repeated boots are a no-op.
func Migrate(ctx context.Context, db *DB, logger *slog.Logger) error {
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("store: goose dialect: %w", err)
	}
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(gooseLogger{logger})

	// goose speaks database/sql; this wraps the existing pgx pool rather than
	// opening a second connection to the same database.
	sqlDB := stdlib.OpenDBFromPool(db.pool)
	defer sqlDB.Close()

	if err := guardWipe(ctx, sqlDB); err != nil {
		return err
	}

	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		return fmt.Errorf("store: migrating: %w", err)
	}
	return nil
}

// gooseLogger adapts goose's printf-style logger onto slog so migration output
// lands in the same structured stream as everything else.
type gooseLogger struct{ logger *slog.Logger }

func (l gooseLogger) Printf(format string, v ...any) {
	l.logger.Info("migration", "message", trimNewline(fmt.Sprintf(format, v...)))
}

// Fatalf must not call os.Exit: Migrate returns its error to the caller, which
// decides whether the process should stop.
func (l gooseLogger) Fatalf(format string, v ...any) {
	l.logger.Error("migration", "message", trimNewline(fmt.Sprintf(format, v...)))
}

// goose writes printf-style lines that already end in a newline; slog adds its
// own, so the trailing one is stripped.
func trimNewline(s string) string { return strings.TrimRight(s, "\r\n") }

// WipeVersion is the migration that drops the previous money model, and
// ConfirmWipeEnv the variable that must name it before an existing database is
// taken across it.
const (
	WipeVersion    = 11
	ConfirmWipeEnv = "COREBANK_CONFIRM_WIPE"
)

// ErrWipeNotConfirmed stops the API from booting into a migration that would
// delete an existing database's financial data without anyone having said so.
var ErrWipeNotConfirmed = errors.New("store: migration 00011 deletes all financial data")

// guardWipe refuses to migrate a database that has data from before
// WipeVersion unless ConfirmWipeEnv says "00011". A fresh database (no goose
// table yet, or version 0) migrates freely: there is nothing to lose.
func guardWipe(ctx context.Context, db *sql.DB) error {
	var exists bool
	if err := db.QueryRowContext(ctx,
		`SELECT to_regclass('goose_db_version') IS NOT NULL`).Scan(&exists); err != nil {
		return fmt.Errorf("store: checking the schema version: %w", err)
	}
	if !exists {
		return nil
	}
	var version int64
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		return fmt.Errorf("store: reading the schema version: %w", err)
	}
	if version == 0 || version >= WipeVersion {
		return nil
	}
	if os.Getenv(ConfirmWipeEnv) != fmt.Sprintf("%05d", WipeVersion) {
		return fmt.Errorf("%w; the database is at version %d. Take a backup, then set %s=%05d to proceed",
			ErrWipeNotConfirmed, version, ConfirmWipeEnv, WipeVersion)
	}
	return nil
}
