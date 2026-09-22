package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestJSONLinesRoutingAndSecretMasking(t *testing.T) {
	var stdout, stderr bytes.Buffer
	logger := NewLogger("debug", &stdout, &stderr, "super-secret")
	logger.Debug("debug event")
	logger.Info("connected", slog.String("request_id", "r1"), slog.String("api_key", "super-secret"), slog.String("detail", "value=super-secret"))
	logger.Warn("upstream issue", slog.String("password", "dont-print"))
	logger.Error("auth issue super-secret")
	if strings.Contains(stderr.String(), "debug event") || !strings.Contains(stdout.String(), "debug event") {
		t.Fatalf("debug not routed to stdout: out=%q err=%q", stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "dont-print") || strings.Contains(stdout.String(), "super-secret") {
		t.Fatalf("stdout leaks secret: %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "dont-print") || strings.Contains(stderr.String(), "super-secret") {
		t.Fatalf("stderr leaks secret: %q", stderr.String())
	}
	for _, stream := range []string{stdout.String(), stderr.String()} {
		for _, line := range strings.Split(strings.TrimSpace(stream), "\n") {
			if line == "" {
				continue
			}
			var record map[string]any
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				t.Fatalf("invalid JSON line %q: %v", line, err)
			}
		}
	}
}

func TestLevelFiltering(t *testing.T) {
	var out, errOut bytes.Buffer
	l := NewLogger("warn", &out, &errOut)
	l.Debug("hidden debug")
	l.Info("hidden info")
	l.Warn("visible warning")
	if out.Len() != 0 || !strings.Contains(errOut.String(), "visible warning") || strings.Contains(errOut.String(), "hidden") {
		t.Fatalf("level filter out=%q err=%q", out.String(), errOut.String())
	}
}
