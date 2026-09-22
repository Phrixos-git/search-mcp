// Package indexer manages the Meilisearch pages index and its asynchronous tasks.
package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const IndexUID = "pages"

type Client struct {
	baseURL      string
	apiKey       string
	http         *http.Client
	pollInterval time.Duration
	waitTimeout  time.Duration
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("Meilisearch HTTP %d: %s", e.Status, e.Message) }

func NewClient(baseURL, apiKey string, httpClient *http.Client, pollInterval, waitTimeout time.Duration) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("Meilisearch URL must be absolute HTTP(S)")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	if waitTimeout <= 0 {
		waitTimeout = 30 * time.Second
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: httpClient, pollInterval: pollInterval, waitTimeout: waitTimeout}, nil
}

func (c *Client) request(ctx context.Context, method, path string, payload any) (map[string]json.RawMessage, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(data))}
	}
	result := map[string]json.RawMessage{}
	if len(data) > 0 {
		if err = json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func rawInt(m map[string]json.RawMessage, key string) (int64, error) {
	var n int64
	err := json.Unmarshal(m[key], &n)
	return n, err
}
func rawString(m map[string]json.RawMessage, key string) (string, error) {
	var s string
	err := json.Unmarshal(m[key], &s)
	return s, err
}

func (c *Client) EnsureIndex(ctx context.Context) error {
	_, err := c.ensureIndex(ctx)
	return err
}

// ensureIndex reports whether it had to create the index. Callers that treat
// SQLite as the source of truth can then rebuild remote state when it was lost.
func (c *Client) ensureIndex(ctx context.Context) (bool, error) {
	info, err := c.request(ctx, http.MethodGet, "/indexes/"+IndexUID, nil)
	createdIndex := false
	if err != nil {
		var api *APIError
		if !errors.As(err, &api) || api.Status != http.StatusNotFound {
			return false, err
		}
		created, e := c.request(ctx, http.MethodPost, "/indexes", map[string]any{"uid": IndexUID, "primaryKey": "id"})
		if e != nil {
			return false, e
		}
		uid, e := rawInt(created, "taskUid")
		if e != nil {
			return false, e
		}
		if e = c.WaitTask(ctx, uid); e != nil {
			return false, e
		}
		createdIndex = true
	} else if pk := info["primaryKey"]; len(pk) > 0 {
		key, _ := rawString(info, "primaryKey")
		if key != "" && key != "id" {
			return false, fmt.Errorf("index %q has primary key %q, expected id", IndexUID, key)
		}
	}
	settings := map[string]any{"searchableAttributes": []string{"title", "content"}, "filterableAttributes": []string{"language", "domain", "content_type"}, "sortableAttributes": []string{"fetched_at", "updated_at"}}
	task, e := c.request(ctx, http.MethodPatch, "/indexes/"+IndexUID+"/settings", settings)
	if e != nil {
		return false, e
	}
	uid, e := rawInt(task, "taskUid")
	if e != nil {
		return false, e
	}
	return createdIndex, c.WaitTask(ctx, uid)
}

type Document struct {
	ID           int64
	Title        string
	URL          string
	CanonicalURL string
	Content      string
	Language     string
	ContentType  string
	Domain       string
	FetchedAt    time.Time
	UpdatedAt    time.Time
}

// SearchHit is a ranked document returned by the pages index.
type SearchHit struct {
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Snippet   string    `json:"snippet"`
	FetchedAt time.Time `json:"fetched_at"`
}

func (c *Client) Enqueue(ctx context.Context, d Document) (int64, error) {
	page := map[string]any{"id": d.ID, "title": d.Title, "url": d.URL, "canonical_url": d.CanonicalURL, "content": d.Content, "language": d.Language, "content_type": d.ContentType, "domain": d.Domain, "fetched_at": d.FetchedAt.UTC().Format(time.RFC3339), "updated_at": d.UpdatedAt.UTC().Format(time.RFC3339)}
	result, err := c.request(ctx, http.MethodPost, "/indexes/"+IndexUID+"/documents?primaryKey=id", []any{page})
	if err != nil {
		return 0, err
	}
	return rawInt(result, "taskUid")
}

// Search queries the pages index and preserves Meilisearch ranking order.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	result, err := c.request(ctx, http.MethodPost, "/indexes/"+IndexUID+"/search", map[string]any{"q": query, "limit": limit})
	if err != nil {
		return nil, err
	}
	if len(result["hits"]) == 0 {
		return []SearchHit{}, nil
	}
	var hits []struct {
		Title     string    `json:"title"`
		URL       string    `json:"url"`
		Content   string    `json:"content"`
		FetchedAt time.Time `json:"fetched_at"`
	}
	if err := json.Unmarshal(result["hits"], &hits); err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, len(hits))
	for _, hit := range hits {
		out = append(out, SearchHit{Title: hit.Title, URL: hit.URL, Snippet: hit.Content, FetchedAt: hit.FetchedAt})
	}
	return out, nil
}

// Health checks Meilisearch's lightweight health endpoint.
func (c *Client) Health(ctx context.Context) error {
	_, err := c.request(ctx, http.MethodGet, "/health", nil)
	return err
}

func (c *Client) WaitTask(ctx context.Context, uid int64) error {
	waitCtx, cancel := context.WithTimeout(ctx, c.waitTimeout)
	defer cancel()
	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	for {
		result, err := c.request(waitCtx, http.MethodGet, "/tasks/"+strconv.FormatInt(uid, 10), nil)
		if err != nil {
			return err
		}
		status, _ := rawString(result, "status")
		switch status {
		case "succeeded":
			return nil
		case "failed", "canceled":
			message := "task " + status
			if raw := result["error"]; len(raw) > 0 {
				var detail map[string]json.RawMessage
				_ = json.Unmarshal(raw, &detail)
				if text, e := rawString(detail, "message"); e == nil && text != "" {
					message = text
				}
			}
			return errors.New(message)
		}
		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-ticker.C:
		}
	}
}
