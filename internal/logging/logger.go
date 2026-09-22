// Package logging provides JSON Lines logging with level routing and secret redaction.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// NewLogger routes debug/info to stdout and warn/error to stderr. Exact secret
// values are removed from messages and string attributes before serialization.
func NewLogger(level string, stdout, stderr io.Writer, secrets ...string) *slog.Logger {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	minLevel := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		minLevel = slog.LevelDebug
	case "warn":
		minLevel = slog.LevelWarn
	case "error":
		minLevel = slog.LevelError
	}
	options := &slog.HandlerOptions{Level: slog.LevelDebug}
	return slog.New(&splitHandler{stdout: slog.NewJSONHandler(stdout, options), stderr: slog.NewJSONHandler(stderr, options), minLevel: minLevel, secrets: append([]string(nil), secrets...)})
}

type splitHandler struct {
	stdout, stderr slog.Handler
	minLevel       slog.Level
	secrets        []string
}

func (h *splitHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if level < h.minLevel {
		return false
	}
	if level >= slog.LevelWarn {
		return h.stderr.Enabled(ctx, level)
	}
	return h.stdout.Enabled(ctx, level)
}
func (h *splitHandler) Handle(ctx context.Context, record slog.Record) error {
	copy := slog.NewRecord(record.Time, record.Level, redact(record.Message, h.secrets), record.PC)
	record.Attrs(func(a slog.Attr) bool { copy.AddAttrs(h.cleanAttr(a)); return true })
	if record.Level >= slog.LevelWarn {
		return h.stderr.Handle(ctx, copy)
	}
	return h.stdout.Handle(ctx, copy)
}
func (h *splitHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = h.cleanAttr(a)
	}
	return &splitHandler{stdout: h.stdout.WithAttrs(clean), stderr: h.stderr.WithAttrs(clean), minLevel: h.minLevel, secrets: h.secrets}
}
func (h *splitHandler) WithGroup(name string) slog.Handler {
	return &splitHandler{stdout: h.stdout.WithGroup(name), stderr: h.stderr.WithGroup(name), minLevel: h.minLevel, secrets: h.secrets}
}
func (h *splitHandler) cleanAttr(a slog.Attr) slog.Attr {
	if sensitive(a.Key) {
		return slog.String(a.Key, "[REDACTED]")
	}
	a.Value = a.Value.Resolve()
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(redact(a.Value.String(), h.secrets))
	case slog.KindGroup:
		values := a.Value.Group()
		for i := range values {
			values[i] = h.cleanAttr(values[i])
		}
		a.Value = slog.GroupValue(values...)
	case slog.KindAny:
		if value, ok := a.Value.Any().(string); ok {
			a.Value = slog.StringValue(redact(value, h.secrets))
		} else {
			a.Value = slog.StringValue(redact(fmt.Sprint(a.Value.Any()), h.secrets))
		}
	}
	return a
}
func sensitive(key string) bool {
	k := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), ".", "_"))
	for _, part := range []string{"secret", "token", "password", "passwd", "api_key", "apikey", "authorization", "cookie", "credential", "private_key", "master_key"} {
		if strings.Contains(k, part) {
			return true
		}
	}
	return false
}
func redact(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}
