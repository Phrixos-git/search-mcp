package main

import (
	"context"
	"log"
	"os"

	"search-mcp/internal/config"
	"search-mcp/internal/requestctx"
)

func main() {
	if _, err := config.Load(); err != nil {
		log.New(os.Stderr, "search-mcp: ", 0).Printf("configuration error: %v", err)
		os.Exit(1)
	}

	// The executable is intentionally limited to configuration bootstrap in
	// Phase 1. HTTP and MCP wiring will be added in later phases.
	_, _ = requestctx.EnsureRequestID(context.Background())
}
