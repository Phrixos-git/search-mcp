// Package metrics keeps bounded-cardinality, in-process operational counters.
package metrics

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Registry records service counters without request identifiers, URLs, queries, or content.
type Registry struct {
	toolCalls          atomic.Uint64
	successes          atomic.Uint64
	failures           atomic.Uint64
	timeouts           atomic.Uint64
	cacheHits          atomic.Uint64
	cacheMisses        atomic.Uint64
	totalDurationNanos atomic.Uint64
	mu                 sync.RWMutex
	errorsByCode       map[string]uint64
	tools              map[string]ToolCounts
	dependencies       map[string]bool
	unsynced           atomic.Int64
	syncFailures       atomic.Uint64
}

type ToolCounts struct {
	Success uint64 `json:"success"`
	Failure uint64 `json:"failure"`
}
type Snapshot struct {
	ToolCalls               uint64                `json:"tool_calls"`
	Successes               uint64                `json:"successes"`
	Failures                uint64                `json:"failures"`
	Timeouts                uint64                `json:"timeouts"`
	CacheHits               uint64                `json:"cache_hits"`
	CacheMisses             uint64                `json:"cache_misses"`
	TotalDuration           time.Duration         `json:"total_duration"`
	ErrorsByCode            map[string]uint64     `json:"errors_by_code"`
	Tools                   map[string]ToolCounts `json:"tools"`
	Dependencies            map[string]bool       `json:"dependencies"`
	UnsyncedDocuments       int64                 `json:"unsynced_documents"`
	MeilisearchSyncFailures uint64                `json:"meilisearch_sync_failures"`
}

func New() *Registry {
	return &Registry{errorsByCode: map[string]uint64{}, tools: map[string]ToolCounts{}, dependencies: map[string]bool{}}
}

func (r *Registry) RecordTool(tool, status, errorCode string, duration time.Duration) {
	if r == nil {
		return
	}
	r.toolCalls.Add(1)
	r.totalDurationNanos.Add(uint64(max(duration, 0)))
	r.mu.Lock()
	counts := r.tools[tool]
	if status == "success" {
		r.successes.Add(1)
		counts.Success++
	} else {
		r.failures.Add(1)
		counts.Failure++
		if errorCode != "" {
			r.errorsByCode[errorCode]++
			if errorCode == "timeout" {
				r.timeouts.Add(1)
			}
		}
	}
	r.tools[tool] = counts
	r.mu.Unlock()
}
func (r *Registry) RecordCache(hit bool) {
	if r == nil {
		return
	}
	if hit {
		r.cacheHits.Add(1)
	} else {
		r.cacheMisses.Add(1)
	}
}
func (r *Registry) RecordSyncFailure() {
	if r != nil {
		r.syncFailures.Add(1)
	}
}
func (r *Registry) SetUnsynced(count int64) {
	if r != nil {
		r.unsynced.Store(max(count, 0))
	}
}
func (r *Registry) SetDependency(name string, healthy bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.dependencies[name] = healthy
	r.mu.Unlock()
}

func (r *Registry) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{}
	}
	r.mu.RLock()
	errorsCopy := make(map[string]uint64, len(r.errorsByCode))
	for k, v := range r.errorsByCode {
		errorsCopy[k] = v
	}
	toolsCopy := make(map[string]ToolCounts, len(r.tools))
	for k, v := range r.tools {
		toolsCopy[k] = v
	}
	depsCopy := make(map[string]bool, len(r.dependencies))
	for k, v := range r.dependencies {
		depsCopy[k] = v
	}
	r.mu.RUnlock()
	return Snapshot{ToolCalls: r.toolCalls.Load(), Successes: r.successes.Load(), Failures: r.failures.Load(), Timeouts: r.timeouts.Load(), CacheHits: r.cacheHits.Load(), CacheMisses: r.cacheMisses.Load(), TotalDuration: time.Duration(r.totalDurationNanos.Load()), ErrorsByCode: errorsCopy, Tools: toolsCopy, Dependencies: depsCopy, UnsyncedDocuments: r.unsynced.Load(), MeilisearchSyncFailures: r.syncFailures.Load()}
}

// ErrorCodes returns a stable, sorted list of observed error labels.
func (s Snapshot) ErrorCodes() []string {
	out := make([]string, 0, len(s.ErrorsByCode))
	for code := range s.ErrorsByCode {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

var Default = New()
