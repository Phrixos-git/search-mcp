package storage

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, e := Open(context.Background(), ":memory:", 5*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestSaveUpdateUniqueAndIndexedHash(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	d := Document{OriginalURL: "https://EXAMPLE.com/?utm_x=1", Title: "first", Content: "body", ETag: "tag"}
	id, e := s.Save(ctx, d)
	if e != nil {
		t.Fatal(e)
	}
	got, ok, e := s.Get(ctx, "https://example.com/")
	if e != nil || !ok {
		t.Fatalf("get %v %v", ok, e)
	}
	if got.ID != id || got.IndexedHash != "" {
		t.Fatalf("unexpected document %#v", got)
	}
	d.Title = "updated"
	_, e = s.Save(ctx, d)
	if e != nil {
		t.Fatal(e)
	}
	got, ok, e = s.Get(ctx, "https://example.com/")
	if e != nil || !ok || got.Title != "updated" {
		t.Fatalf("update %#v %v", got, e)
	}
}
func TestTransactionRollback(t *testing.T) {
	s := openTest(t)
	expected := errors.New("abort")
	err := s.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, e := tx.Exec("INSERT INTO documents(original_url,canonical_url,created_at,updated_at) VALUES('x','https://x/',1,1)")
		if e != nil {
			return e
		}
		return expected
	})
	if !errors.Is(err, expected) {
		t.Fatalf("got %v", err)
	}
	if _, ok, e := s.Get(context.Background(), "https://x/"); e != nil || ok {
		t.Fatalf("rollback failed: %v %v", ok, e)
	}
}
func TestCachePolicies(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name             string
		h                http.Header
		expires          time.Time
		noCache, noStore bool
	}{
		{"fallback", http.Header{}, now.Add(24 * time.Hour), false, false},
		{"max-age", http.Header{"Cache-Control": []string{"max-age=60"}}, now.Add(time.Minute), false, false},
		{"expires", http.Header{"Expires": []string{now.Add(time.Hour).Format(http.TimeFormat)}}, now.Add(time.Hour), false, false},
		{"no-cache", http.Header{"Cache-Control": []string{"no-cache"}}, now.Add(24 * time.Hour), true, false},
		{"no-store", http.Header{"Cache-Control": []string{"no-store"}}, now.Add(24 * time.Hour), false, true},
	}
	for _, tt := range tests {
		p := EvaluateCache(tt.h, now)
		if !p.ExpiresAt.Equal(tt.expires) || p.NoCache != tt.noCache || p.NoStore != tt.noStore {
			t.Errorf("%s: %#v", tt.name, p)
		}
	}
	d := Document{ETag: "e", LastModified: "date"}
	if ConditionalHeaders(d).Get("If-None-Match") != "e" || ConditionalHeaders(d).Get("If-Modified-Since") != "" {
		t.Fatal("ETag should take precedence")
	}
	d.ETag = ""
	if ConditionalHeaders(d).Get("If-Modified-Since") != "date" {
		t.Fatal("missing Last-Modified conditional")
	}
}
func TestCacheFresh(t *testing.T) {
	now := time.Now()
	d := Document{ExpiresAt: now.Add(time.Hour)}
	if !CacheFresh(d, now, false) || CacheFresh(d, now, true) {
		t.Fatal("fresh/no-cache decision failed")
	}
	d.ExpiresAt = now
	if CacheFresh(d, now, false) {
		t.Fatal("expired entry was fresh")
	}
	d.ExpiresAt = now.Add(time.Hour)
	d.NoCache = true
	if CacheFresh(d, now, false) {
		t.Fatal("no-cache entry was reused without revalidation")
	}
}

func TestSaveResponseNoStore(t *testing.T) {
	s := openTest(t)
	policy := EvaluateCache(http.Header{"Cache-Control": []string{"no-store"}}, time.Now())
	if _, err := s.SaveResponse(context.Background(), Document{OriginalURL: "https://private.test/"}, policy); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Get(context.Background(), "https://private.test/"); err != nil || ok {
		t.Fatalf("no-store response persisted: ok=%v err=%v", ok, err)
	}
}

func TestSyncStateLifecycle(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, err := s.Save(ctx, Document{OriginalURL: "https://sync.test/", Content: "v1", ContentHash: "hash-v1"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.Unsynced(ctx)
	if err != nil || len(pending) != 1 || pending[0].ID != id {
		t.Fatalf("unsynced: %#v, %v", pending, err)
	}
	if err := s.SetTask(ctx, id, 25); err != nil {
		t.Fatal(err)
	}
	d, ok, err := s.GetByID(ctx, id)
	if err != nil || !ok || d.MeiliTaskUID == nil || *d.MeiliTaskUID != 25 {
		t.Fatalf("task state: %#v, %v", d, err)
	}
	if err := s.CompleteTask(ctx, id, 25, "hash-v1"); err != nil {
		t.Fatal(err)
	}
	d, _, _ = s.GetByID(ctx, id)
	if d.IndexedHash != "hash-v1" || d.IndexedAt == nil || d.MeiliTaskUID != nil {
		t.Fatalf("successful state: %#v", d)
	}
	pending, err = s.Unsynced(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("synced document remained pending: %#v, %v", pending, err)
	}
	if _, err := s.Save(ctx, Document{OriginalURL: "https://sync.test/", Content: "v2", ContentHash: "hash-v2"}); err != nil {
		t.Fatal(err)
	}
	pending, err = s.Unsynced(ctx)
	if err != nil || len(pending) != 1 || pending[0].ContentHash != "hash-v2" {
		t.Fatalf("changed content not pending: %#v, %v", pending, err)
	}
	all, err := s.AllDocuments(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("full list: %#v, %v", all, err)
	}
	if err := s.SetTask(ctx, id, 26); err != nil {
		t.Fatal(err)
	}
	if err := s.FailTask(ctx, id, 26, "task failed"); err != nil {
		t.Fatal(err)
	}
	d, _, _ = s.GetByID(ctx, id)
	if d.IndexedHash != "" || d.IndexError != "task failed" || d.MeiliTaskUID != nil {
		t.Fatalf("failed state advanced index: %#v", d)
	}
}
