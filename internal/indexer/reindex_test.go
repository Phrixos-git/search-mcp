package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"search-mcp/internal/storage"
	"testing"
	"time"
)

func TestFullReindexRebuildsEverySQLiteDocument(t *testing.T) {
	store, e := storage.Open(context.Background(), ":memory:", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	for _, u := range []string{"https://one.test/", "https://two.test/"} {
		if _, e = store.Save(context.Background(), storage.Document{OriginalURL: u, Content: "content", ContentHash: u}); e != nil {
			t.Fatal(e)
		}
	}
	docs, e := store.AllDocuments(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for i, d := range docs {
		if e = store.SetTask(context.Background(), d.ID, int64(90+i)); e != nil {
			t.Fatal(e)
		}
		if e = store.CompleteTask(context.Background(), d.ID, int64(90+i), d.ContentHash); e != nil {
			t.Fatal(e)
		}
	}
	taskUID := int64(1)
	enqueued := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "DELETE /indexes/pages":
			w.Write([]byte("{\"taskUid\":1}"))
		case "GET /tasks/1":
			w.Write([]byte("{\"status\":\"succeeded\"}"))
		case "GET /indexes/pages":
			http.Error(w, "not found", 404)
		case "POST /indexes":
			taskUID = 2
			w.Write([]byte("{\"taskUid\":2}"))
		case "GET /tasks/2":
			w.Write([]byte("{\"status\":\"succeeded\"}"))
		case "PATCH /indexes/pages/settings":
			taskUID = 3
			w.Write([]byte("{\"taskUid\":3}"))
		case "GET /tasks/3":
			w.Write([]byte("{\"status\":\"succeeded\"}"))
		case "POST /indexes/pages/documents":
			var payload []map[string]any
			if e := json.NewDecoder(r.Body).Decode(&payload); e != nil {
				t.Error(e)
			}
			enqueued += len(payload)
			taskUID++
			fmt.Fprintf(w, "{\"taskUid\":%d}", taskUID)
		default:
			if r.Method == "GET" && r.URL.Path == fmt.Sprintf("/tasks/%d", taskUID) {
				w.Write([]byte("{\"status\":\"succeeded\"}"))
				return
			}
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	defer server.Close()
	c, e := NewClient(server.URL, "", server.Client(), time.Millisecond, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.FullReindex(context.Background(), store); e != nil {
		t.Fatal(e)
	}
	if enqueued != 2 {
		t.Fatalf("full reindex enqueued %d docs", enqueued)
	}
	docs, e = store.AllDocuments(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range docs {
		if d.IndexedHash != d.ContentHash || d.IndexedAt == nil {
			t.Errorf("not indexed %#v", d)
		}
	}
}
