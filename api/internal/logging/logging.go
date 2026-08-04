// Package logging builds the process logger and carries it through context.
//
// Everything logs through log/slog. Passing the logger in the request context
// rather than reaching for a package-level global is what makes every line
// emitted while handling a request carry its request id automatically: the
// service layer logs without knowing anything about HTTP, and the correlation
// still holds.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// New builds the root logger. format is "json" or "text"; level is one of
// debug, info, warn, error.
func New(level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type loggerKey struct{}

// WithLogger returns a context carrying logger.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// FromContext returns the context's logger, falling back to the default one so
// a caller that was handed a bare context still logs instead of panicking.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
