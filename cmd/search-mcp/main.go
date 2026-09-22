package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"search-mcp/internal/config"
	"search-mcp/internal/fetch"
	"search-mcp/internal/indexer"
	"search-mcp/internal/mcp"
	"search-mcp/internal/search"
	"search-mcp/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.New(os.Stderr, "search-mcp: ", 0).Printf("configuration error: %v", err)
		os.Exit(1)
	}

	logger := log.New(os.Stderr, "search-mcp: ", 0)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
	web, err := search.NewSearXNG(cfg.SearXNGBaseURL, &http.Client{Timeout: cfg.SearXNGTimeout}, cfg.SearXNGTimeout)
	if err != nil {
		logger.Printf("SearXNG configuration error: %v", err)
		os.Exit(1)
	}
	pages := fetch.NewFetcher(store, client, cfg.FetchTimeout, cfg.FetchMaxResponseBytes)
	server := mcp.NewServer(web, client, pages)
	addr := fmt.Sprintf("%s:%d", cfg.MCPHost, cfg.MCPPort)
	logger.Printf("starting stateless Streamable HTTP MCP server on %s", addr)
	if err := mcp.Serve(ctx, addr, mcp.NewHTTPHandler(server)); err != nil {
		logger.Printf("HTTP server error: %v", err)
		os.Exit(1)
	}
}
