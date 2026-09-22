package mcp

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
	"search-mcp/internal/fetch"
	"search-mcp/internal/indexer"
	"search-mcp/internal/requestctx"
	"search-mcp/internal/search"
)

func startHTTPTestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &httptest.Server{Config: &http.Server{Handler: h}, Listener: l}
	s.Start()
	t.Cleanup(s.Close)
	return s
}

type localStub struct {
	t         *testing.T
	requestID string
}

func (s localStub) Search(ctx context.Context, query string, limit int) ([]indexer.SearchHit, error) {
	if requestctx.RequestID(ctx) != s.requestID || requestctx.ToolCallID(ctx) == "" {
		s.t.Errorf("missing request metadata")
	}
	return []indexer.SearchHit{{Title: "title", URL: "https://example.test/", Snippet: "snippet"}}, nil
}

type fetchStub struct{}

func (fetchStub) Fetch(context.Context, string) (fetch.Result, error) {
	return fetch.Result{Title: "page"}, nil
}

type addRequestID struct{ base http.RoundTripper }

func (r addRequestID) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.Header = req.Header.Clone()
	copy.Header.Set("X-Request-ID", "known-id")
	return r.base.RoundTrip(copy)
}

func TestStreamableStatelessDiscoveryAndToolCall(t *testing.T) {
	webServer := startHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[],"unresponsive_engines":[]}`))
	}))
	web, _ := search.NewSearXNG(webServer.URL, webServer.Client(), 0)
	handler := startHTTPTestServer(t, NewHTTPHandler(NewServer(web, localStub{t, "known-id"}, fetchStub{})))
	client := protocol.NewClient(&protocol.Implementation{Name: "test", Version: "1"}, nil)
	httpClient := &http.Client{Transport: addRequestID{base: http.DefaultTransport}}
	session, err := client.Connect(context.Background(), &protocol.StreamableClientTransport{Endpoint: handler.URL + "/mcp", HTTPClient: httpClient, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 3 {
		t.Fatalf("discovered %d tools", len(tools.Tools))
	}
	for _, name := range []string{"search_web", "search_local", "fetch_page"} {
		found := false
		for _, tool := range tools.Tools {
			if tool.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("missing tool %q", name)
		}
	}
	res, err := session.CallTool(context.Background(), &protocol.CallToolParams{Name: "search_local", Arguments: map[string]any{"query": "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || res.StructuredContent == nil {
		t.Fatalf("tool response: %+v", res)
	}
	webRes, err := session.CallTool(context.Background(), &protocol.CallToolParams{Name: "search_web", Arguments: map[string]any{"query": ""}})
	if err != nil || !webRes.IsError || webRes.StructuredContent == nil {
		t.Fatalf("common error response: result=%+v err=%v", webRes, err)
	}
}

func TestRequestIDHeaderEcho(t *testing.T) {
	web, _ := search.NewSearXNG("http://127.0.0.1:1", nil, 0)
	h := NewHTTPHandler(NewServer(web, localStub{t, "known-id"}, fetchStub{}))
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("X-Request-ID", "known-id")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got := w.Header().Get("X-Request-ID"); got != "known-id" {
		t.Fatalf("request id=%q", got)
	}
}
