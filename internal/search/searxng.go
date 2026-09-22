package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"search-mcp/internal/toolerror"
	"search-mcp/internal/urlutil"
)

const (
	DefaultMaxResults = 5
	MaxResults        = 10
	MaxQueryRunes     = 256
	MaxSnippetRunes   = 256
)

type Result struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	Snippet   string `json:"snippet"`
	FetchedAt string `json:"fetched_at,omitempty"`
}
type Input struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
}

func (in Input) Validate() (Input, *toolerror.Error) {
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" || utf8.RuneCountInString(in.Query) > MaxQueryRunes {
		return in, toolerror.Invalid("query must contain between 1 and 256 characters")
	}
	if in.MaxResults == 0 {
		in.MaxResults = DefaultMaxResults
	}
	if in.MaxResults < 1 || in.MaxResults > MaxResults {
		return in, toolerror.Invalid("max_results must be between 1 and 10")
	}
	return in, nil
}

func clip(s string, n int) string {
	r := []rune(strings.ToValidUTF8(s, ""))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

type SearXNG struct {
	baseURL  string
	endpoint string
	client   *http.Client
	timeout  time.Duration
}

func NewSearXNG(baseURL string, client *http.Client, timeout time.Duration) (*SearXNG, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("SearXNG URL must be absolute HTTP(S)")
	}
	if timeout <= 0 {
		timeout = 18 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	baseURL = strings.TrimRight(u.String(), "/")
	u.Path = strings.TrimRight(u.Path, "/") + "/search"
	return &SearXNG{baseURL: baseURL, endpoint: u.String(), client: client, timeout: timeout}, nil
}

// Health checks SearXNG without launching a search across external engines.
func (s *SearXNG) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("SearXNG health HTTP %d", resp.StatusCode)
	}
	return nil
}

type searxResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
		Engine  string `json:"engine"`
	} `json:"results"`
	Unresponsive [][]json.RawMessage `json:"unresponsive_engines"`
}

func (s *SearXNG) Search(ctx context.Context, input Input) ([]Result, bool, *toolerror.Error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	input, invalid := input.Validate()
	if invalid != nil {
		return nil, false, invalid
	}
	form := url.Values{"q": {input.Query}, "format": {"json"}}
	var payload searxResponse
	var last *toolerror.Error
	err := toolerror.Do(ctx, func(ctx context.Context) error {
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewBufferString(form.Encode()))
		if e != nil {
			return e
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		resp, e := s.client.Do(req)
		if e != nil {
			last = toolerror.FromNetwork(e)
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			last = toolerror.FromHTTP(resp.StatusCode, resp.Header.Get("Retry-After"))
			return fmt.Errorf("SearXNG status %d", resp.StatusCode)
		}
		data, e := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if e != nil {
			last = toolerror.FromNetwork(e)
			return e
		}
		if e = json.Unmarshal(data, &payload); e != nil {
			last = &toolerror.Error{Code: toolerror.Upstream, Message: "SearXNG returned invalid JSON", Retryable: true}
			return e
		}
		return nil
	}, func(err error) *toolerror.Error {
		if last != nil {
			return last
		}
		return toolerror.FromNetwork(err)
	})
	if err != nil {
		return nil, false, err
	}
	if len(payload.Results) == 0 && len(payload.Unresponsive) > 0 {
		return nil, false, &toolerror.Error{Code: toolerror.Upstream, Message: "all SearXNG engines failed", Retryable: true}
	}
	out := make([]Result, 0, min(len(payload.Results), input.MaxResults))
	seen := map[string]struct{}{}
	for _, item := range payload.Results {
		if strings.TrimSpace(item.Title) == "" {
			continue
		}
		canonical, e := urlutil.Normalize(item.URL)
		if e != nil {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, Result{Title: item.Title, URL: item.URL, Snippet: clip(item.Content, MaxSnippetRunes)})
		if len(out) == input.MaxResults {
			break
		}
	}
	return out, len(payload.Unresponsive) > 0, nil
}
