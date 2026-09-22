package indexer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"search-mcp/internal/storage"
	"testing"
	"time"
)

func TestSyncSuccessOnlyAdvancesAfterTaskSuccess(t *testing.T) {
	store, e := storage.Open(context.Background(), ":memory:", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	id, e := store.Save(context.Background(), storage.Document{OriginalURL: "https://sync.test/", Content: "body", ContentHash: "hash1"})
	if e != nil {
		t.Fatal(e)
	}
	processing := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /indexes/pages/documents":
			w.Write([]byte("{\"taskUid\":11}"))
		case "GET /tasks/11":
			if !processing {
				d, _, err := store.GetByID(context.Background(), id)
				if err != nil || d.IndexedHash != "" {
					t.Errorf("indexed before success: %#v %v", d, err)
				}
				processing = true
				w.Write([]byte("{\"status\":\"processing\"}"))
				return
			}
			w.Write([]byte("{\"status\":\"succeeded\"}"))
		default:
			http.Error(w, "unexpected", 404)
		}
	}))
	defer server.Close()
	c, e := NewClient(server.URL, "", server.Client(), time.Millisecond, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	d, _, _ := store.GetByID(context.Background(), id)
	if e = c.SyncDocument(context.Background(), store, d); e != nil {
		t.Fatal(e)
	}
	d, _, _ = store.GetByID(context.Background(), id)
	if d.IndexedHash != "hash1" || d.MeiliTaskUID != nil {
		t.Fatalf("completed state %#v", d)
	}
}
func TestStartupSyncProcessesOnlyUnsyncedDocuments(t *testing.T) {
	store, err := storage.Open(context.Background(), ":memory:", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id1, err := store.Save(context.Background(), storage.Document{OriginalURL: "https://done.test/", Content: "done", ContentHash: "done-hash"})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := store.Save(context.Background(), storage.Document{OriginalURL: "https://todo.test/", Content: "todo", ContentHash: "todo-hash"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetTask(context.Background(), id1, 80); err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteTask(context.Background(), id1, 80, "done-hash"); err != nil {
		t.Fatal(err)
	}
	enqueued := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /indexes/pages":
			w.Write([]byte("{\"uid\":\"pages\",\"primaryKey\":\"id\"}"))
		case "PATCH /indexes/pages/settings":
			w.Write([]byte("{\"taskUid\":10}"))
		case "GET /tasks/10":
			w.Write([]byte("{\"status\":\"succeeded\"}"))
		case "POST /indexes/pages/documents":
			var docs []map[string]any
			if err := json.NewDecoder(r.Body).Decode(&docs); err != nil {
				t.Error(err)
			}
			if len(docs) != 1 || docs[0]["id"] != float64(id2) {
				t.Errorf("unexpected differential payload: %#v", docs)
			}
			enqueued += len(docs)
			w.Write([]byte("{\"taskUid\":11}"))
		case "GET /tasks/11":
			w.Write([]byte("{\"status\":\"succeeded\"}"))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "", server.Client(), time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.StartupSync(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if enqueued != 1 {
		t.Fatalf("expected only one document, got %d", enqueued)
	}
}

func TestFailedTaskDoesNotAdvanceHash(t *testing.T) {
	store, e := storage.Open(context.Background(), ":memory:", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	id, e := store.Save(context.Background(), storage.Document{OriginalURL: "https://failed.test/", Content: "body", ContentHash: "hash1"})
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.Write([]byte("{\"taskUid\":12}"))
			return
		}
		w.Write([]byte("{\"status\":\"failed\",\"error\":{\"message\":\"invalid document\"}}"))
	}))
	defer server.Close()
	c, e := NewClient(server.URL, "", server.Client(), time.Millisecond, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	d, _, _ := store.GetByID(context.Background(), id)
	if e = c.SyncDocument(context.Background(), store, d); e == nil {
		t.Fatal("expected failed task")
	}
	d, _, _ = store.GetByID(context.Background(), id)
	if d.IndexedHash != "" || d.IndexError != "invalid document" || d.MeiliTaskUID != nil {
		t.Fatalf("failed state %#v", d)
	}
}

func TestStartupSyncRebuildsDocumentsWhenRemoteIndexWasLost(t *testing.T) {
	store, err := storage.Open(context.Background(), ":memory:", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id, err := store.Save(context.Background(), storage.Document{OriginalURL: "https://recovery.test/", Content: "retained", ContentHash: "hash"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetTask(context.Background(), id, 1); err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteTask(context.Background(), id, 1, "hash"); err != nil {
		t.Fatal(err)
	}
	indexed := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /indexes/pages":
			http.Error(w, "missing", http.StatusNotFound)
		case "POST /indexes":
			w.Write([]byte(`{"taskUid":10}`))
		case "PATCH /indexes/pages/settings":
			w.Write([]byte(`{"taskUid":11}`))
		case "GET /tasks/10", "GET /tasks/11", "GET /tasks/12":
			w.Write([]byte(`{"status":"succeeded"}`))
		case "POST /indexes/pages/documents":
			var docs []map[string]any
			if e := json.NewDecoder(r.Body).Decode(&docs); e != nil {
				t.Error(e)
			}
			if len(docs) != 1 || docs[0]["id"] != float64(id) || docs[0]["content"] != "retained" {
				t.Errorf("rebuild payload: %#v", docs)
			}
			indexed = true
			w.Write([]byte(`{"taskUid":12}`))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer api.Close()
	client, err := NewClient(api.URL, "", api.Client(), time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.StartupSync(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatal("missing remote index was not repopulated from SQLite")
	}
	d, _, err := store.GetByID(context.Background(), id)
	if err != nil || d.IndexedHash != "hash" {
		t.Fatalf("document was not marked indexed after remote success: %#v %v", d, err)
	}
}
