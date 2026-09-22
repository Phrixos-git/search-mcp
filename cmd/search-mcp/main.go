package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"search-mcp/internal/config"
	"search-mcp/internal/fetch"
	"search-mcp/internal/indexer"
	"search-mcp/internal/logging"
	"search-mcp/internal/mcp"
	"search-mcp/internal/metrics"
	"search-mcp/internal/search"
	"search-mcp/internal/server"
	"search-mcp/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		logging.NewLogger("error", os.Stdout, os.Stderr).Error("configuration error", "error", err)
		os.Exit(1)
	}

	logger := logging.NewLogger(cfg.LogLevel, os.Stdout, os.Stderr, cfg.MeiliMasterKey)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := storage.Open(ctx, cfg.SQLiteDBPath, cfg.SQLiteBusyTimeout)
	if err != nil {
		logger.Error("SQLite initialization error", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	client, err := indexer.NewClient(cfg.MeilisearchURL, cfg.MeiliMasterKey, nil, cfg.MeiliTaskPollInterval, cfg.MeiliTaskWatchTimeout)
	if err != nil {
		logger.Error("Meilisearch configuration error", "error", err)
		os.Exit(1)
	}
	if len(os.Args) > 1 && os.Args[1] == "reindex" {
		reindexCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		if err := client.FullReindex(reindexCtx, store); err != nil {
			logger.Error("full reindex failed", "error", err)
			os.Exit(1)
		}
		logger.Info("full reindex completed", "event", "full_reindex_completed")
		return
	}
	startupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := client.StartupSync(startupCtx, store); err != nil {
		logger.Error("Meilisearch startup synchronization error", "error", err)
		os.Exit(1)
	}
	web, err := search.NewSearXNG(cfg.SearXNGBaseURL, &http.Client{Timeout: cfg.SearXNGTimeout}, cfg.SearXNGTimeout)
	if err != nil {
		logger.Error("SearXNG configuration error", "error", err)
		os.Exit(1)
	}
	pages := fetch.NewFetcher(store, client, cfg.FetchTimeout, cfg.FetchMaxResponseBytes)
	registry := metrics.Default
	toolServer := mcp.NewServer(web, client, pages, mcp.Options{Logger: logger, Metrics: registry})
	handler := server.NewHandler(mcp.NewHTTPHandler(toolServer), store.DB(),
		server.ProbeFunc(client.Health), server.ProbeFunc(web.Health))
	handler.SetMetrics(registry)
	addr := fmt.Sprintf("%s:%d", cfg.MCPHost, cfg.MCPPort)
	logger.Info("starting stateless Streamable HTTP MCP server", "event", "server_started", "address", addr)
	if err := mcp.Serve(ctx, addr, handler, logger); err != nil {
		logger.Error("HTTP server error", "error", err)
		os.Exit(1)
	}
}
