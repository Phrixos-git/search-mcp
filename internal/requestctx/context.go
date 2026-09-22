// Package requestctx provides common request and tool-call context metadata.
package requestctx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

type contextKey uint8

const (
	requestIDKey contextKey = iota + 1
	toolCallIDKey
)

var fallbackSequence uint64

// WithRequestID stores an orchestrator-provided request ID in ctx.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(nonNilContext(ctx), requestIDKey, requestID)
}

// RequestID returns the request ID stored in ctx, or an empty string.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

// EnsureRequestID preserves an existing request ID or generates one.
func EnsureRequestID(ctx context.Context) (context.Context, string) {
	if requestID := RequestID(ctx); requestID != "" {
		return nonNilContext(ctx), requestID
	}
	requestID := NewID("req")
	return WithRequestID(ctx, requestID), requestID
}

// WithToolCallID stores a Search MCP tool-call ID in ctx.
func WithToolCallID(ctx context.Context, toolCallID string) context.Context {
	return context.WithValue(nonNilContext(ctx), toolCallIDKey, toolCallID)
}

// ToolCallID returns the tool-call ID stored in ctx, or an empty string.
func ToolCallID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(toolCallIDKey).(string)
	return value
}

// EnsureToolCallID preserves an existing tool-call ID or generates one.
func EnsureToolCallID(ctx context.Context) (context.Context, string) {
	if toolCallID := ToolCallID(ctx); toolCallID != "" {
		return nonNilContext(ctx), toolCallID
	}
	toolCallID := NewID("tool")
	return WithToolCallID(ctx, toolCallID), toolCallID
}

// NewID returns a non-secret opaque identifier suitable for log correlation.
func NewID(prefix string) string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		sequence := atomic.AddUint64(&fallbackSequence, 1)
		return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), sequence)
	}
	return prefix + "-" + hex.EncodeToString(raw[:])
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
