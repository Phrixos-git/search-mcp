package indexer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc, poll, timeout time.Duration) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c, err := NewClient(server.URL, "mock-key", server.Client(), poll, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEnsureIndexAndDocumentEnqueue(t *testing.T) {
	var settingsSeen, docSeen bool
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mock-key" {
			t.Errorf("authorization missing")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /indexes/pages":
			http.Error(w, "missing", http.StatusNotFound)
		case "POST /indexes":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["uid"] != "pages" || payload["primaryKey"] != "id" {
				t.Errorf("create payload: %#v", payload)
			}
			w.Write([]byte("{\"taskUid\":1}"))
		case "GET /tasks/1":
			w.Write([]byte("{\"status\":\"succeeded\"}"))
		case "PUT /indexes/pages/settings":
			var payload map[string][]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if strings.Join(payload["searchableAttributes"], ",") != "title,content" {
				t.Errorf("settings: %#v", payload)
			}
			settingsSeen = true
			w.Write([]byte("{\"taskUid\":2}"))
		case "GET /tasks/2":
			w.Write([]byte("{\"status\":\"succeeded\"}"))
		case "POST /indexes/pages/documents":
			var payload []map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if len(payload) != 1 || payload[0]["id"] != float64(42) {
				t.Errorf("documents: %#v", payload)
			}
			docSeen = true
			w.Write([]byte("{\"taskUid\":3}"))
		default:
			http.Error(w, r.Method+" "+r.URL.String(), http.StatusNotFound)
		}
	}, time.Millisecond, time.Second)
	if err := c.EnsureIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	uid, err := c.Enqueue(context.Background(), Document{ID: 42, Title: "t", URL: "https://a.test", CanonicalURL: "https://a.test/", FetchedAt: time.Now()})
	if err != nil || uid != 3 {
		t.Fatalf("enqueue %d %v", uid, err)
	}
	if !settingsSeen || !docSeen {
		t.Fatal("expected operations were not observed")
	}
}

func TestSearchPreservesMeilisearchRanking(t *testing.T) {
	var seen bool
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/indexes/pages/search" {
			t.Errorf("request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input["q"] != "needle" || input["limit"] != float64(2) {
			t.Errorf("payload=%#v", input)
		}
		seen = true
		w.Write([]byte(`{"hits":[{"title":"rank 1","url":"https://a.test/","content":"a","fetched_at":"2026-09-22T10:00:00Z"},{"title":"rank 2","url":"https://b.test/","content":"b","fetched_at":"2026-09-22T11:00:00Z"}]}`))
	}, time.Millisecond, time.Second)
	hits, err := c.Search(context.Background(), "needle", 2)
	if err != nil || !seen || len(hits) != 2 || hits[0].Title != "rank 1" || hits[1].Title != "rank 2" {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
}

func TestSearchEmptyIsNormal(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"hits":[]}`)) }, time.Millisecond, time.Second)
	hits, err := c.Search(context.Background(), "absent", 5)
	if err != nil || hits == nil || len(hits) != 0 {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
}

func TestWaitTaskFailed(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{\"status\":\"failed\",\"error\":{\"message\":\"bad document\"}}"))
	}, time.Millisecond, time.Second)
	if err := c.WaitTask(context.Background(), 9); err == nil || !strings.Contains(err.Error(), "bad document") {
		t.Fatalf("got %v", err)
	}
}

func TestWaitTaskTimeout(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{\"status\":\"processing\"}")) }, time.Millisecond, 15*time.Millisecond)
	if err := c.WaitTask(context.Background(), 9); err == nil {
		t.Fatal("expected timeout")
	}
}
