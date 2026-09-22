package indexer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"search-mcp/internal/storage"
)

func toIndexDocument(d storage.Document) Document {
	return Document{ID: d.ID, Title: d.Title, URL: d.CanonicalURL, CanonicalURL: d.CanonicalURL, Content: d.Content, Language: d.Language, ContentType: d.ContentType, Domain: d.Domain, FetchedAt: d.FetchedAt, UpdatedAt: time.Now()}
}

// SyncDocument advances indexed_hash only after Meilisearch confirms task success.
func (c *Client) SyncDocument(ctx context.Context, store *storage.Store, d storage.Document) error {
	task, err := c.Enqueue(ctx, toIndexDocument(d))
	if err != nil {
		_ = store.SetIndexError(ctx, d.ID, err.Error())
		return err
	}
	if err = store.SetTask(ctx, d.ID, task); err != nil {
		return err
	}
	if err = c.WaitTask(ctx, task); err != nil {
		_ = store.FailTask(ctx, d.ID, task, err.Error())
		return err
	}
	return store.CompleteTask(ctx, d.ID, task, d.ContentHash)
}

func (c *Client) SyncPending(ctx context.Context, store *storage.Store) error {
	pending, err := store.Unsynced(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range pending {
		if e := c.SyncDocument(ctx, store, d); e != nil {
			errs = append(errs, fmt.Errorf("document %d: %w", d.ID, e))
		}
	}
	return errors.Join(errs...)
}

// FullReindex deletes and recreates the remote index, then rebuilds it from SQLite.
func (c *Client) FullReindex(ctx context.Context, store *storage.Store) error {
	result, err := c.request(ctx, http.MethodDelete, "/indexes/"+IndexUID, nil)
	if err != nil {
		var api *APIError
		if !errors.As(err, &api) || api.Status != http.StatusNotFound {
			return err
		}
	}
	if _, ok := result["taskUid"]; ok {
		uid, e := rawInt(result, "taskUid")
		if e != nil {
			return e
		}
		if e = c.WaitTask(ctx, uid); e != nil {
			return e
		}
	}
	if err = store.ResetIndexState(ctx); err != nil {
		return err
	}
	if err = c.EnsureIndex(ctx); err != nil {
		return err
	}
	return c.SyncPending(ctx, store)
}

func (c *Client) StartupSync(ctx context.Context, store *storage.Store) error {
	if err := c.EnsureIndex(ctx); err != nil {
		return err
	}
	return c.SyncPending(ctx, store)
}
