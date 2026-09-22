package search

import (
	"context"
	"net/http"
	"testing"
	"time"

	"search-mcp/internal/indexer"
	"search-mcp/internal/toolerror"
)

type localSearchFunc func(context.Context, string, int) ([]indexer.SearchHit, error)

func (f localSearchFunc) Search(ctx context.Context, q string, n int) ([]indexer.SearchHit, error) {
	return f(ctx, q, n)
}

func TestLocalSearchSuccessEmptyValidationAndSnippetLimit(t *testing.T) {
	client := localSearchFunc(func(_ context.Context, q string, n int) ([]indexer.SearchHit, error) {
		if q != "q" || n != 5 {
			t.Errorf("query=%q limit=%d", q, n)
		}
		return []indexer.SearchHit{{Title: "a", URL: "https://a.test", Snippet: string(make([]rune, 0))}}, nil
	})
	got, e := LocalSearch(context.Background(), client, Input{Query: " q "})
	if e != nil || len(got) != 1 {
		t.Fatalf("got=%v err=%v", got, e)
	}
	empty, _ := LocalSearch(context.Background(), localSearchFunc(func(context.Context, string, int) ([]indexer.SearchHit, error) { return []indexer.SearchHit{}, nil }), Input{Query: "none"})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty=%v", empty)
	}
	if _, e = LocalSearch(context.Background(), client, Input{}); e == nil || e.Code != "invalid_argument" {
		t.Fatalf("validation=%v", e)
	}
	long := make([]rune, 257)
	for i := range long {
		long[i] = '界'
	}
	got, e = LocalSearch(context.Background(), localSearchFunc(func(context.Context, string, int) ([]indexer.SearchHit, error) {
		return []indexer.SearchHit{{Snippet: string(long)}}, nil
	}), Input{Query: "q"})
	if e != nil || len([]rune(got[0].Snippet)) != 256 {
		t.Fatalf("snippet length=%d err=%v", len([]rune(got[0].Snippet)), e)
	}
}

func TestLocalSearchMeiliFailureAndTimeout(t *testing.T) {
	got, e := LocalSearchWithTimeout(context.Background(), localSearchFunc(func(context.Context, string, int) ([]indexer.SearchHit, error) {
		return nil, &indexer.APIError{Status: http.StatusBadRequest, Message: "bad request"}
	}), Input{Query: "q"}, time.Second)
	if got != nil || e == nil || e.Code != "upstream_error" || e.Retryable {
		t.Fatalf("got=%v error=%+v", got, e)
	}
	got, e = LocalSearchWithTimeout(context.Background(), localSearchFunc(func(ctx context.Context, _ string, _ int) ([]indexer.SearchHit, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}), Input{Query: "q"}, 10*time.Millisecond)
	if got != nil || e == nil || e.Code != "timeout" {
		t.Fatalf("got=%v error=%+v", got, e)
	}
	status := toolerror.FromHTTP(http.StatusServiceUnavailable, "")
	if !status.Retryable {
		t.Fatal("5xx should be retryable")
	}
}
