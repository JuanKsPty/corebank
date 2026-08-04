package store

import (
	"context"
	"fmt"
	"log/slog"
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
