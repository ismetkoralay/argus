// Package logging provides Argus's structured logger: a JSON slog.Logger
// with a redaction guard, level-controlled verbosity, and a context-carried
// correlation logger so a single review's log lines all share the same
// delivery ID / repo / PR / head SHA fields without threading a logger
// through every interface in the request path.
package logging

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

// sensitiveKeySubstrings are matched case-insensitively against an
// attribute's key. Any match redacts the value before it reaches the
// handler, regardless of which package logged it. This is defense-in-depth:
// nothing in Argus logs these fields today, but a future call site
// accidentally including one (e.g. logging a whole config struct) should
// not leak it.
var sensitiveKeySubstrings = []string{
	"secret",
	"private_key",
	"privatekey",
	"token",
	"password",
	"apikey",
}

const redacted = "[REDACTED]"

// New builds Argus's structured logger: JSON output to w, level parsed from
// level ("debug", "info", "warn"/"warning", "error"; anything else,
// including empty, defaults to info), with the redaction guard applied.
func New(level string, w io.Writer) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       parseLevel(level),
		ReplaceAttr: redactAttr,
	})
	return slog.New(handler)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func redactAttr(_ []string, a slog.Attr) slog.Attr {
	key := strings.ToLower(a.Key)
	for _, s := range sensitiveKeySubstrings {
		if strings.Contains(key, s) {
			a.Value = slog.StringValue(redacted)
			return a
		}
	}
	return a
}

// ctxKey is the unexported type used to store a *slog.Logger on a
// context.Context, so it can't collide with keys from other packages.
type ctxKey struct{}

// WithLogger returns a copy of ctx carrying logger, retrievable by
// FromContext. Each layer in the request path (webhook, orchestrator) calls
// this after adding the correlation fields it uniquely knows, so every
// logger further down the call chain picks them up.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, logger)
}

// FromContext returns the logger stored in ctx by WithLogger. If none is
// present, it returns fallback[0] if given and non-nil, else slog.Default().
func FromContext(ctx context.Context, fallback ...*slog.Logger) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	if len(fallback) > 0 && fallback[0] != nil {
		return fallback[0]
	}
	return slog.Default()
}
