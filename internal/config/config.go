// Package config loads and validates Search MCP process configuration.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultSearXNGBaseURL        = "http://searxng:8080"
	defaultMeilisearchURL        = "http://meilisearch:7700"
	defaultSQLiteDBPath          = "/data/search.db"
	defaultMCPHost               = "0.0.0.0"
	defaultMCPPort               = 8081
	defaultLogLevel              = "info"
	defaultSQLiteBusyTimeout     = 5 * time.Second
	defaultSearXNGTimeout        = 18 * time.Second
	defaultFetchTimeout          = 18 * time.Second
	defaultFetchMaxResponseBytes = int64(5 * 1024 * 1024)
	defaultCacheFallbackTTL      = 24 * time.Hour
	defaultMeiliPollInterval     = time.Second
	defaultMeiliWatchTimeout     = 30 * time.Second
	defaultSnippetMaxChars       = 256
)

// Config contains process configuration. Secret values are kept in memory
// only and are never intended for logging or serialization.
type Config struct {
	SearXNGBaseURL        string
	MeilisearchURL        string
	SQLiteDBPath          string
	MCPHost               string
	MCPPort               int
	LogLevel              string
	SQLiteBusyTimeout     time.Duration
	SearXNGTimeout        time.Duration
	FetchTimeout          time.Duration
	FetchMaxResponseBytes int64
	CacheFallbackTTL      time.Duration
	MeiliTaskPollInterval time.Duration
	MeiliTaskWatchTimeout time.Duration
	SnippetMaxChars       int
	MeiliMasterKeyFile    string
	MeiliMasterKey        string
}

// EnvGetter and SecretReader make configuration loading deterministic in tests
// while keeping the production path backed by the process environment and
// filesystem.
type EnvGetter func(name string) string
type SecretReader func(path string) ([]byte, error)

// Load reads configuration from the process environment and Docker Secret
// files.
func Load() (Config, error) {
	return LoadFrom(os.Getenv, os.ReadFile)
}

// LoadFrom reads configuration using the supplied environment and secret
// readers. Secrets are accepted only through *_FILE variables.
func LoadFrom(getEnv EnvGetter, readSecret SecretReader) (Config, error) {
	if getEnv == nil {
		return Config{}, fmt.Errorf("environment reader is nil")
	}
	if readSecret == nil {
		return Config{}, fmt.Errorf("secret reader is nil")
	}
	if direct := getEnv("MEILI_MASTER_KEY"); direct != "" {
		return Config{}, fmt.Errorf("MEILI_MASTER_KEY is not supported; use MEILI_MASTER_KEY_FILE")
	}

	c := Config{
		SearXNGBaseURL:     valueOr(getEnv, "SEARXNG_BASE_URL", defaultSearXNGBaseURL),
		MeilisearchURL:     valueOr(getEnv, "MEILISEARCH_URL", defaultMeilisearchURL),
		SQLiteDBPath:       valueOr(getEnv, "SQLITE_DB_PATH", defaultSQLiteDBPath),
		MCPHost:            valueOr(getEnv, "MCP_HOST", defaultMCPHost),
		LogLevel:           strings.ToLower(valueOr(getEnv, "LOG_LEVEL", defaultLogLevel)),
		MeiliMasterKeyFile: strings.TrimSpace(getEnv("MEILI_MASTER_KEY_FILE")),
	}

	var err error
	if c.MCPPort, err = intEnv(getEnv, "MCP_PORT", defaultMCPPort); err != nil {
		return Config{}, err
	}
	if c.SQLiteBusyTimeout, err = durationEnv(getEnv, "SQLITE_BUSY_TIMEOUT_MS", defaultSQLiteBusyTimeout); err != nil {
		return Config{}, err
	}
	if c.SearXNGTimeout, err = durationEnv(getEnv, "SEARXNG_TIMEOUT_MS", defaultSearXNGTimeout); err != nil {
		return Config{}, err
	}
	if c.FetchTimeout, err = durationEnv(getEnv, "FETCH_TIMEOUT_MS", defaultFetchTimeout); err != nil {
		return Config{}, err
	}
	if c.CacheFallbackTTL, err = durationValueEnv(getEnv, "CACHE_FALLBACK_TTL", defaultCacheFallbackTTL); err != nil {
		return Config{}, err
	}
	if c.MeiliTaskPollInterval, err = durationValueEnv(getEnv, "MEILI_TASK_POLL_INTERVAL", defaultMeiliPollInterval); err != nil {
		return Config{}, err
	}
	if c.MeiliTaskWatchTimeout, err = durationValueEnv(getEnv, "MEILI_TASK_WATCH_TIMEOUT", defaultMeiliWatchTimeout); err != nil {
		return Config{}, err
	}
	if c.FetchMaxResponseBytes, err = int64Env(getEnv, "FETCH_MAX_RESPONSE_BYTES", defaultFetchMaxResponseBytes); err != nil {
		return Config{}, err
	}
	if c.SnippetMaxChars, err = intEnv(getEnv, "SNIPPET_MAX_CHARS", defaultSnippetMaxChars); err != nil {
		return Config{}, err
	}

	if err := c.validate(); err != nil {
		return Config{}, err
	}

	if c.MeiliMasterKeyFile == "" {
		return Config{}, fmt.Errorf("MEILI_MASTER_KEY_FILE is required")
	}
	secret, err := readSecret(c.MeiliMasterKeyFile)
	if err != nil {
		return Config{}, fmt.Errorf("read MEILI_MASTER_KEY_FILE: %w", err)
	}
	c.MeiliMasterKey = strings.TrimRight(string(secret), "\r\n")
	if c.MeiliMasterKey == "" {
		return Config{}, fmt.Errorf("MEILI_MASTER_KEY_FILE contains an empty secret")
	}

	return c, nil
}

func (c Config) validate() error {
	for name, raw := range map[string]string{
		"SEARXNG_BASE_URL": c.SearXNGBaseURL,
		"MEILISEARCH_URL":  c.MeilisearchURL,
	} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("%s must be an absolute HTTP(S) URL", name)
		}
	}
	if c.SQLiteDBPath == "" {
		return fmt.Errorf("SQLITE_DB_PATH must not be empty")
	}
	if c.MCPHost == "" {
		return fmt.Errorf("MCP_HOST must not be empty")
	}
	if c.MCPPort < 1 || c.MCPPort > 65535 {
		return fmt.Errorf("MCP_PORT must be between 1 and 65535")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("LOG_LEVEL must be one of debug, info, warn, error")
	}
	if c.FetchMaxResponseBytes <= 0 {
		return fmt.Errorf("FETCH_MAX_RESPONSE_BYTES must be positive")
	}
	if c.SnippetMaxChars <= 0 {
		return fmt.Errorf("SNIPPET_MAX_CHARS must be positive")
	}
	return nil
}

func valueOr(getEnv EnvGetter, name, fallback string) string {
	if value := getEnv(name); value != "" {
		return value
	}
	return fallback
}

func intEnv(getEnv EnvGetter, name string, fallback int) (int, error) {
	raw := getEnv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return value, nil
}

func int64Env(getEnv EnvGetter, name string, fallback int64) (int64, error) {
	raw := getEnv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return value, nil
}

func durationEnv(getEnv EnvGetter, name string, fallback time.Duration) (time.Duration, error) {
	raw := getEnv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	value *= int64(time.Millisecond)
	if value <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return time.Duration(value), nil
}

func durationValueEnv(getEnv EnvGetter, name string, fallback time.Duration) (time.Duration, error) {
	raw := getEnv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return value, nil
}
