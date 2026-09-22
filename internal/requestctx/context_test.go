package requestctx

import (
	"context"
	"testing"
)

func TestEnsureIDs(t *testing.T) {
	ctx, requestID := EnsureRequestID(nil)
	if requestID == "" || RequestID(ctx) != requestID {
		t.Fatalf("request ID was not stored: %q", requestID)
	}
	ctx, toolCallID := EnsureToolCallID(ctx)
	if toolCallID == "" || ToolCallID(ctx) != toolCallID {
		t.Fatalf("tool-call ID was not stored: %q", toolCallID)
	}
	if _, nextRequestID := EnsureRequestID(WithRequestID(ctx, "orchestrator-request")); nextRequestID != "orchestrator-request" {
		t.Fatalf("existing request ID was replaced: %q", nextRequestID)
	}
}

func TestIDsAreScoped(t *testing.T) {
	ctx := context.Background()
	if RequestID(ctx) != "" || ToolCallID(ctx) != "" {
		t.Fatal("empty context unexpectedly contained IDs")
	}
}
