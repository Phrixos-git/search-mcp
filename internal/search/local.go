package search

import (
	"context"
	"errors"
	"net"
	"time"

	"search-mcp/internal/indexer"
	"search-mcp/internal/toolerror"
)

type LocalHit struct {
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Snippet   string    `json:"snippet"`
	FetchedAt time.Time `json:"fetched_at"`
}
type LocalSearcher interface {
	Search(context.Context, string, int) ([]indexer.SearchHit, error)
}

// LocalSearch validates the common query contract and maps ranked Meilisearch hits.
func LocalSearch(ctx context.Context, client LocalSearcher, input Input) ([]LocalHit, *toolerror.Error) {
	return LocalSearchWithTimeout(ctx, client, input, 10*time.Second)
}

func LocalSearchWithTimeout(ctx context.Context, client LocalSearcher, input Input, timeout time.Duration) ([]LocalHit, *toolerror.Error) {
	in, invalid := input.Validate()
	if invalid != nil {
		return nil, invalid
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var hits []indexer.SearchHit
	err := toolerror.Do(ctx, func(ctx context.Context) error {
		var err error
		hits, err = client.Search(ctx, in.Query, in.MaxResults)
		return err
	}, func(err error) *toolerror.Error {
		var api *indexer.APIError
		if errors.As(err, &api) {
			return toolerror.FromHTTP(api.Status, "")
		}
		var ne net.Error
		if errors.As(err, &ne) {
			return toolerror.FromNetwork(err)
		}
		return &toolerror.Error{Code: toolerror.Upstream, Message: "Meilisearch search failed", Retryable: true}
	})
	if err != nil {
		return nil, err
	}
	out := make([]LocalHit, 0, len(hits))
	for _, hit := range hits {
		out = append(out, LocalHit{Title: hit.Title, URL: hit.URL, Snippet: clip(hit.Snippet, MaxSnippetRunes), FetchedAt: hit.FetchedAt})
	}
	return out, nil
}
