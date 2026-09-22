// Package mcp wires the three search tools to a stateless Streamable HTTP server.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
	"search-mcp/internal/fetch"
	"search-mcp/internal/requestctx"
	"search-mcp/internal/search"
	"search-mcp/internal/toolerror"
)

type SearchOutput struct {
	Results []search.Result  `json:"results"`
	Partial bool             `json:"partial,omitempty"`
	Error   *toolerror.Error `json:"error,omitempty"`
}
type LocalOutput struct {
	Results []search.LocalHit `json:"results"`
	Error   *toolerror.Error  `json:"error,omitempty"`
}
type FetchOutput struct {
	Result *fetch.Result    `json:"result,omitempty"`
	Error  *toolerror.Error `json:"error,omitempty"`
}

type fetcher interface {
	Fetch(context.Context, string) (fetch.Result, error)
}

func NewServer(web *search.SearXNG, local search.LocalSearcher, pages fetcher) *protocol.Server {
	s := protocol.NewServer(&protocol.Implementation{Name: "search-mcp", Version: "0.1.0"}, nil)
	protocol.AddTool(s, &protocol.Tool{Name: "search_web", Description: "Search the web using SearXNG."}, func(ctx context.Context, _ *protocol.CallToolRequest, in search.Input) (*protocol.CallToolResult, SearchOutput, error) {
		ctx = callContext(ctx)
		results, partial, e := web.Search(ctx, in)
		out := SearchOutput{Results: results, Partial: partial, Error: e}
		return toolResult(e), out, nil
	})
	protocol.AddTool(s, &protocol.Tool{Name: "search_local", Description: "Search pages saved in the local index."}, func(ctx context.Context, _ *protocol.CallToolRequest, in search.Input) (*protocol.CallToolResult, LocalOutput, error) {
		ctx, _ = requestctx.EnsureRequestID(ctx)
		ctx, _ = requestctx.EnsureToolCallID(ctx)
		results, e := search.LocalSearch(ctx, local, in)
		out := LocalOutput{Results: results, Error: e}
		return toolResult(e), out, nil
	})
	protocol.AddTool(s, &protocol.Tool{Name: "fetch_page", Description: "Fetch and extract a web page."}, func(ctx context.Context, _ *protocol.CallToolRequest, in fetch.Input) (*protocol.CallToolResult, FetchOutput, error) {
		ctx, _ = requestctx.EnsureRequestID(ctx)
		ctx, _ = requestctx.EnsureToolCallID(ctx)
		if e := in.Validate(); e != nil {
			mapped := toolerror.Invalid(e.Error())
			return toolResult(mapped), FetchOutput{Error: mapped}, nil
		}
		result, e := pages.Fetch(ctx, in.URL)
		var mapped *toolerror.Error
		if e != nil {
			var fetchErr *fetch.FetchError
			if errors.As(e, &fetchErr) {
				switch fetchErr.Code {
				case "blocked_address", "blocked_redirect":
					mapped = &toolerror.Error{Code: toolerror.AccessDenied, Message: "the target address is not allowed"}
				case "response_too_large", "unsupported_content_type":
					mapped = &toolerror.Error{Code: toolerror.Upstream, Message: "upstream response cannot be processed"}
				case "rate_limited":
					mapped = toolerror.FromCode(toolerror.RateLimited, fetchErr.RetryAfterSeconds)
				case "timeout":
					mapped = toolerror.FromCode(toolerror.Timeout, 0)
				case "not_found":
					mapped = toolerror.FromCode(toolerror.NotFound, 0)
				case "access_denied":
					mapped = toolerror.FromCode(toolerror.AccessDenied, 0)
				case "invalid_argument":
					mapped = toolerror.Invalid("URL is invalid")
				case "internal_error":
					mapped = toolerror.FromCode(toolerror.Internal, 0)
				default:
					mapped = toolerror.FromCode(toolerror.Upstream, 0)
				}
			} else {
				mapped = toolerror.FromNetwork(e)
			}
		}
		return toolResult(mapped), FetchOutput{Result: &result, Error: mapped}, nil
	})
	return s
}

func callContext(ctx context.Context) context.Context {
	ctx, _ = requestctx.EnsureRequestID(ctx)
	ctx, _ = requestctx.EnsureToolCallID(ctx)
	return ctx
}
func toolResult(e *toolerror.Error) *protocol.CallToolResult {
	if e == nil {
		return nil
	}
	b, _ := json.Marshal(e)
	return &protocol.CallToolResult{IsError: true, Content: []protocol.Content{&protocol.TextContent{Text: string(b)}}}
}

// NewHTTPHandler mounts MCP at /mcp and initializes request correlation metadata.
func NewHTTPHandler(server *protocol.Server) http.Handler {
	mux := http.NewServeMux()
	transport := protocol.NewStreamableHTTPHandler(func(*http.Request) *protocol.Server { return server }, &protocol.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux.Handle("/mcp", transport)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rid := strings.TrimSpace(r.Header.Get("X-Request-ID")); rid != "" {
			r = r.WithContext(requestctx.WithRequestID(r.Context(), rid))
		} else {
			ctx, _ := requestctx.EnsureRequestID(r.Context())
			r = r.WithContext(ctx)
		}
		w.Header().Set("X-Request-ID", requestctx.RequestID(r.Context()))
		mux.ServeHTTP(w, r)
	})
}

// Serve starts an HTTP listener and gracefully shuts it down with ctx.
func Serve(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	select {
	case err := <-done:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			return err
		}
		err := <-done
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}
