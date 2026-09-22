package metrics

import (
	"testing"
	"time"
)

func TestRegistrySnapshotHasBoundedOperationalDimensions(t *testing.T) {
	r := New()
	r.RecordTool("search_local", "success", "", 12*time.Millisecond)
	r.RecordTool("fetch_page", "failure", "timeout", 20*time.Millisecond)
	r.RecordCache(true)
	r.RecordCache(false)
	r.RecordSyncFailure()
	r.SetUnsynced(3)
	r.SetDependency("meilisearch", true)
	s := r.Snapshot()
	if s.ToolCalls != 2 || s.Successes != 1 || s.Failures != 1 || s.Timeouts != 1 || s.CacheHits != 1 || s.CacheMisses != 1 || s.MeilisearchSyncFailures != 1 || s.UnsyncedDocuments != 3 {
		t.Fatalf("unexpected metrics snapshot: %#v", s)
	}
	if !s.Dependencies["meilisearch"] || s.Tools["search_local"].Success != 1 || s.Tools["fetch_page"].Failure != 1 {
		t.Fatalf("unexpected grouped metrics: %#v", s)
	}
}
