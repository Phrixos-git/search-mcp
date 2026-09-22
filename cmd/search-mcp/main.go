package main

import (
	"context"
	"log"
	"os"
	"time"

	"search-mcp/internal/config"
	"search-mcp/internal/indexer"
	"search-mcp/internal/requestctx"
	"search-mcp/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.New(os.Stderr, "search-mcp: ", 0).Printf("configuration error: %v", err)
		os.Exit(1)
	}

	logger := log.New(os.Stderr, "search-mcp: ", 0)
	ctx := context.Background()
	store, err := storage.Open(ctx, cfg.SQLiteDBPath, cfg.SQLiteBusyTimeout)
	if err != nil {
		logger.Printf("SQLite initialization error: %v", err)
		os.Exit(1)
	}
	defer store.Close()
	client, err := indexer.NewClient(cfg.MeilisearchURL, cfg.MeiliMasterKey, nil, cfg.MeiliTaskPollInterval, cfg.MeiliTaskWatchTimeout)
	if err != nil {
		logger.Printf("Meilisearch configuration error: %v", err)
		os.Exit(1)
	}
	startupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := client.StartupSync(startupCtx, store); err != nil {
		logger.Printf("Meilisearch startup synchronization error: %v", err)
		os.Exit(1)
	}
	_, _ = requestctx.EnsureRequestID(ctx)
}
