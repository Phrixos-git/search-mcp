package config

import (
	"errors"
	"testing"
)

func TestLoadFromDefaultsAndSecretFile(t *testing.T) {
	env := map[string]string{"MEILI_MASTER_KEY_FILE": "/run/secrets/meili_master_key"}
	config, err := LoadFrom(envGetter(env), func(path string) ([]byte, error) {
		if path != env["MEILI_MASTER_KEY_FILE"] {
			t.Fatalf("unexpected secret path: %s", path)
		}
		return []byte("unit-test-value\r\n"), nil
	})
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if config.MCPHost != defaultMCPHost || config.MCPPort != defaultMCPPort {
		t.Fatalf("unexpected server defaults: %#v", config)
	}
	if config.FetchMaxResponseBytes != defaultFetchMaxResponseBytes {
		t.Fatalf("unexpected response size: %d", config.FetchMaxResponseBytes)
	}
	if config.MeiliMasterKey != "unit-test-value" {
		t.Fatalf("secret was not stripped correctly: %q", config.MeiliMasterKey)
	}
}

func TestLoadFromOverrides(t *testing.T) {
	env := map[string]string{
		"MEILI_MASTER_KEY_FILE":    "/tmp/key",
		"MCP_PORT":                 "9090",
		"LOG_LEVEL":                "DEBUG",
		"SQLITE_BUSY_TIMEOUT_MS":   "2500",
		"CACHE_FALLBACK_TTL":       "12h",
		"FETCH_MAX_RESPONSE_BYTES": "1024",
	}
	config, err := LoadFrom(envGetter(env), func(string) ([]byte, error) {
		return []byte("value"), nil
	})
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if config.MCPPort != 9090 || config.LogLevel != "debug" {
		t.Fatalf("overrides not applied: %#v", config)
	}
	if config.SQLiteBusyTimeout.Milliseconds() != 2500 || config.CacheFallbackTTL.Hours() != 12 {
		t.Fatalf("duration overrides not applied: %#v", config)
	}
	if config.FetchMaxResponseBytes != 1024 {
		t.Fatalf("size override not applied: %d", config.FetchMaxResponseBytes)
	}
}

func TestLoadFromRejectsMissingSecret(t *testing.T) {
	env := map[string]string{"MEILI_MASTER_KEY_FILE": "/missing/key"}
	_, err := LoadFrom(envGetter(env), func(string) ([]byte, error) {
		return nil, errors.New("not found")
	})
	if err == nil {
		t.Fatal("LoadFrom() unexpectedly accepted a missing secret")
	}
}

func TestLoadFromRejectsDirectSecretEnvironment(t *testing.T) {
	env := map[string]string{
		"MEILI_MASTER_KEY":      "must-not-be-used",
		"MEILI_MASTER_KEY_FILE": "/tmp/key",
	}
	_, err := LoadFrom(envGetter(env), func(string) ([]byte, error) {
		return []byte("value"), nil
	})
	if err == nil {
		t.Fatal("LoadFrom() unexpectedly accepted a direct secret environment variable")
	}
}

func TestLoadFromRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{name: "port", env: map[string]string{"MEILI_MASTER_KEY_FILE": "/tmp/key", "MCP_PORT": "0"}},
		{name: "url", env: map[string]string{"MEILI_MASTER_KEY_FILE": "/tmp/key", "MEILISEARCH_URL": "not-a-url"}},
		{name: "duration", env: map[string]string{"MEILI_MASTER_KEY_FILE": "/tmp/key", "CACHE_FALLBACK_TTL": "nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFrom(envGetter(tt.env), func(string) ([]byte, error) {
				return []byte("value"), nil
			})
			if err == nil {
				t.Fatal("LoadFrom() unexpectedly accepted invalid configuration")
			}
		})
	}
}

func envGetter(values map[string]string) EnvGetter {
	return func(name string) string { return values[name] }
}
