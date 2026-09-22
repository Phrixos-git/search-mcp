// Package server provides health/readiness endpoints and HTTP lifecycle gates.
package server

import (
	"context"
	"errors"
	"net/http"
	"search-mcp/internal/metrics"
	"strconv"
	"sync/atomic"
	"time"
)

// Pinger is satisfied by *sql.DB.
type Pinger interface{ PingContext(context.Context) error }

// Probe reports whether an external dependency is reachable.
type Probe interface{ Check(context.Context) error }

type ProbeFunc func(context.Context) error

func (f ProbeFunc) Check(ctx context.Context) error { return f(ctx) }

// Handler exposes liveness/readiness and mounts the MCP handler at /mcp.
type Handler struct {
	db           Pinger
	probes       []Probe
	mcp          http.Handler
	shuttingDown atomic.Bool
	metrics      *metrics.Registry
}

func NewHandler(mcpHandler http.Handler, db Pinger, dependencies ...Probe) *Handler {
	return &Handler{mcp: mcpHandler, db: db, probes: dependencies}
}

func (h *Handler) SetShuttingDown(value bool) { h.shuttingDown.Store(value) }

func (h *Handler) SetMetrics(registry *metrics.Registry) { h.metrics = registry }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/health":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		if h.db == nil || h.db.PingContext(r.Context()) != nil {
			write(w, http.StatusServiceUnavailable, "unhealthy")
			return
		}
		write(w, http.StatusOK, "ok")
	case "/ready":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		if h.shuttingDown.Load() || h.db == nil || h.db.PingContext(r.Context()) != nil {
			write(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		for i, probe := range h.probes {
			name := "dependency_" + strconv.Itoa(i)
			if i == 0 {
				name = "meilisearch"
			}
			if i == 1 {
				name = "searxng"
			}
			err := error(nil)
			if probe == nil {
				err = errors.New("probe unavailable")
			} else {
				err = probe.Check(ctx)
			}
			if h.metrics != nil {
				h.metrics.SetDependency(name, err == nil)
			}
			if err != nil {
				write(w, http.StatusServiceUnavailable, "unavailable")
				return
			}
		}
		write(w, http.StatusOK, "ok")
	case "/mcp":
		if h.shuttingDown.Load() {
			write(w, http.StatusServiceUnavailable, "shutting down")
			return
		}
		if h.mcp == nil {
			http.NotFound(w, r)
			return
		}
		h.mcp.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", http.MethodGet)
	write(w, http.StatusMethodNotAllowed, "method not allowed")
}
func write(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body + "\n"))
}
