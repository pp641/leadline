// Package logger configures structured JSON logging and carries request-scoped
// attributes (request_id, organization_id, user_id) through context.
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxKey struct{}

// New returns a JSON slog logger at the given level ("debug", "info", "warn", "error").
func New(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl, ReplaceAttr: redact})
	return slog.New(h)
}

// sensitiveKeys are never written to logs, whatever their value.
var sensitiveKeys = map[string]bool{
	"password": true, "password_hash": true, "token": true, "access_token": true,
	"refresh_token": true, "authorization": true, "api_key": true, "secret": true,
	"smtp_password": true, "cookie": true,
}

func redact(_ []string, a slog.Attr) slog.Attr {
	if sensitiveKeys[strings.ToLower(a.Key)] {
		return slog.String(a.Key, "[REDACTED]")
	}
	return a
}

// With returns a context whose logger includes the extra attributes.
func With(ctx context.Context, args ...any) context.Context {
	return context.WithValue(ctx, ctxKey{}, From(ctx).With(args...))
}

// From returns the logger stored in ctx, or the default logger.
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
