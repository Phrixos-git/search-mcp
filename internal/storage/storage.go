// Package storage provides SQLite persistence for fetched documents.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"search-mcp/internal/urlutil"
)

const schema = "CREATE TABLE IF NOT EXISTS documents (" +
	"id INTEGER PRIMARY KEY, original_url TEXT NOT NULL, canonical_url TEXT NOT NULL UNIQUE," +
	"title TEXT NOT NULL DEFAULT '', content TEXT NOT NULL DEFAULT '', content_hash TEXT NOT NULL DEFAULT ''," +
	"content_type TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '', domain TEXT NOT NULL DEFAULT ''," +
	"etag TEXT NOT NULL DEFAULT '', last_modified TEXT NOT NULL DEFAULT '', http_status INTEGER NOT NULL DEFAULT 0," +
	"fetched_at INTEGER NOT NULL DEFAULT 0, expires_at INTEGER NOT NULL DEFAULT 0, indexed_hash TEXT NOT NULL DEFAULT ''," +
	"indexed_at INTEGER, meili_task_uid INTEGER, index_error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);" +
	"CREATE INDEX IF NOT EXISTS documents_expires_at_idx ON documents(expires_at);" +
	"CREATE INDEX IF NOT EXISTS documents_content_hash_idx ON documents(content_hash);"

type Store struct{ db *sql.DB }
type Document struct {
	ID                                                                                    int64
	OriginalURL, CanonicalURL, Title, Content, ContentHash, ContentType, Language, Domain string
	ETag, LastModified                                                                    string
	HTTPStatus                                                                            int
	FetchedAt, ExpiresAt                                                                  time.Time
	IndexedHash, IndexError                                                               string
	IndexedAt                                                                             *time.Time
	MeiliTaskUID                                                                          *int64
	Truncated                                                                             bool
	NoCache                                                                               bool
}

func Open(ctx context.Context, dsn string, busyTimeout time.Duration) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("sqlite DSN is required")
	}
	if busyTimeout <= 0 {
		busyTimeout = 5 * time.Second
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	q := url.Values{}
	q.Set("_pragma", "busy_timeout="+strconv.FormatInt(busyTimeout.Milliseconds(), 10))
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	db, err := sql.Open("sqlite", dsn+sep+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db}
	if err = s.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) DB() *sql.DB  { return s.db }
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)")
	if err != nil {
		return err
	}
	var n int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version=1").Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err = tx.ExecContext(ctx, schema); err != nil {
			return fmt.Errorf("migration 1: %w", err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(1,?)", time.Now().Unix()); err != nil {
			return err
		}
	}
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version=2").Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err = tx.ExecContext(ctx, "ALTER TABLE documents ADD COLUMN truncated INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("migration 2: %w", err)
		}
		if _, err = tx.ExecContext(ctx, "ALTER TABLE documents ADD COLUMN no_cache INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("migration 2: %w", err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(2,?)", time.Now().Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Get(ctx context.Context, canonical string) (Document, bool, error) {
	d, err := scan(s.db.QueryRowContext(ctx, "SELECT id,original_url,canonical_url,title,content,content_hash,content_type,language,domain,etag,last_modified,http_status,fetched_at,expires_at,indexed_hash,indexed_at,meili_task_uid,index_error,truncated,no_cache FROM documents WHERE canonical_url=?", canonical))
	if errors.Is(err, sql.ErrNoRows) {
		return Document{}, false, nil
	}
	if err != nil {
		return Document{}, false, err
	}
	return d, true, nil
}

// GetByID returns one persisted document by its SQLite primary key.
func (s *Store) GetByID(ctx context.Context, id int64) (Document, bool, error) {
	d, err := scan(s.db.QueryRowContext(ctx, "SELECT id,original_url,canonical_url,title,content,content_hash,content_type,language,domain,etag,last_modified,http_status,fetched_at,expires_at,indexed_hash,indexed_at,meili_task_uid,index_error,truncated,no_cache FROM documents WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Document{}, false, nil
	}
	if err != nil {
		return Document{}, false, err
	}
	return d, true, nil
}

func (s *Store) GetByOriginalURL(ctx context.Context, original string) (Document, bool, error) {
	d, err := scan(s.db.QueryRowContext(ctx, "SELECT id,original_url,canonical_url,title,content,content_hash,content_type,language,domain,etag,last_modified,http_status,fetched_at,expires_at,indexed_hash,indexed_at,meili_task_uid,index_error,truncated,no_cache FROM documents WHERE original_url=? ORDER BY id LIMIT 1", original))
	if errors.Is(err, sql.ErrNoRows) {
		return Document{}, false, nil
	}
	if err != nil {
		return Document{}, false, err
	}
	return d, true, nil
}

func (s *Store) DeleteByCanonical(ctx context.Context, canonical string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM documents WHERE canonical_url=?", canonical)
	return err
}

// Unsynced returns documents whose current content has not been indexed.
func (s *Store) Unsynced(ctx context.Context) ([]Document, error) {
	return s.listDocuments(ctx, "WHERE content_hash<>indexed_hash OR indexed_hash='' ORDER BY id")
}

// AllDocuments returns all documents in stable primary-key order.
func (s *Store) AllDocuments(ctx context.Context) ([]Document, error) {
	return s.listDocuments(ctx, "ORDER BY id")
}

func (s *Store) listDocuments(ctx context.Context, suffix string) ([]Document, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,original_url,canonical_url,title,content,content_hash,content_type,language,domain,etag,last_modified,http_status,fetched_at,expires_at,indexed_hash,indexed_at,meili_task_uid,index_error,truncated,no_cache FROM documents "+suffix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Document, 0)
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetTask records the Meilisearch task associated with a document.
func (s *Store) SetTask(ctx context.Context, id, taskUID int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE documents SET meili_task_uid=?,index_error='',updated_at=? WHERE id=?", taskUID, time.Now().Unix(), id)
	return err
}

// CompleteTask marks the exact content snapshot indexed only after task success.
func (s *Store) CompleteTask(ctx context.Context, id, taskUID int64, indexedHash string) error {
	res, err := s.db.ExecContext(ctx, "UPDATE documents SET indexed_hash=?,indexed_at=?,meili_task_uid=NULL,index_error='',updated_at=? WHERE id=? AND meili_task_uid=?", indexedHash, time.Now().Unix(), time.Now().Unix(), id, taskUID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return errors.New("Meilisearch task was superseded before completion")
	}
	return err
}

// FailTask records failure without advancing indexed_hash.
func (s *Store) FailTask(ctx context.Context, id, taskUID int64, cause string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE documents SET meili_task_uid=NULL,index_error=?,updated_at=? WHERE id=? AND meili_task_uid=?", cause, time.Now().Unix(), id, taskUID)
	return err
}

// SetIndexError records failures that occur before Meilisearch returns a task UID.
func (s *Store) SetIndexError(ctx context.Context, id int64, cause string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE documents SET meili_task_uid=NULL,index_error=?,updated_at=? WHERE id=?", cause, time.Now().Unix(), id)
	return err
}

// ResetIndexState marks all documents pending after the remote index is dropped.
func (s *Store) ResetIndexState(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "UPDATE documents SET indexed_hash='',indexed_at=NULL,meili_task_uid=NULL,index_error='',updated_at=?", time.Now().Unix())
	return err
}
func (s *Store) Save(ctx context.Context, d Document) (int64, error) {
	if d.OriginalURL == "" {
		return 0, errors.New("original URL is required")
	}
	canonicalInput := d.CanonicalURL
	if canonicalInput == "" {
		canonicalInput = d.OriginalURL
	}
	canonical, err := urlutil.Normalize(canonicalInput)
	if err != nil {
		return 0, err
	}
	d.CanonicalURL = canonical
	if d.FetchedAt.IsZero() {
		d.FetchedAt = time.Now()
	}
	if d.ExpiresAt.IsZero() {
		d.ExpiresAt = d.FetchedAt.Add(24 * time.Hour)
	}
	now := time.Now().Unix()
	q := "INSERT INTO documents(original_url,canonical_url,title,content,content_hash,content_type,language,domain,etag,last_modified,http_status,fetched_at,expires_at,indexed_hash,indexed_at,meili_task_uid,index_error,truncated,no_cache,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'',NULL,NULL,'',?,?,?,?) ON CONFLICT(canonical_url) DO UPDATE SET original_url=excluded.original_url,title=excluded.title,content=excluded.content,content_hash=excluded.content_hash,content_type=excluded.content_type,language=excluded.language,domain=excluded.domain,etag=excluded.etag,last_modified=excluded.last_modified,http_status=excluded.http_status,fetched_at=excluded.fetched_at,expires_at=excluded.expires_at,truncated=excluded.truncated,no_cache=excluded.no_cache,indexed_hash=CASE WHEN documents.content_hash<>excluded.content_hash THEN '' ELSE documents.indexed_hash END,indexed_at=CASE WHEN documents.content_hash<>excluded.content_hash THEN NULL ELSE documents.indexed_at END,meili_task_uid=CASE WHEN documents.content_hash<>excluded.content_hash THEN NULL ELSE documents.meili_task_uid END,index_error=CASE WHEN documents.content_hash<>excluded.content_hash THEN '' ELSE documents.index_error END,updated_at=excluded.updated_at"
	_, err = s.db.ExecContext(ctx, q, d.OriginalURL, d.CanonicalURL, d.Title, d.Content, d.ContentHash, d.ContentType, d.Language, d.Domain, d.ETag, d.LastModified, d.HTTPStatus, d.FetchedAt.Unix(), d.ExpiresAt.Unix(), d.Truncated, d.NoCache, now, now)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM documents WHERE canonical_url=?", d.CanonicalURL).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// SaveResponse skips persistence when Cache-Control explicitly says no-store.
func (s *Store) SaveResponse(ctx context.Context, d Document, policy CachePolicy) (int64, error) {
	if policy.NoStore {
		return 0, nil
	}
	d.ExpiresAt = policy.ExpiresAt
	d.ETag, d.LastModified = policy.ETag, policy.LastModified
	d.NoCache = policy.NoCache
	return s.Save(ctx, d)
}
func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
func scan(r interface{ Scan(...any) error }) (Document, error) {
	var d Document
	var f, e int64
	var ix, task sql.NullInt64
	err := r.Scan(&d.ID, &d.OriginalURL, &d.CanonicalURL, &d.Title, &d.Content, &d.ContentHash, &d.ContentType, &d.Language, &d.Domain, &d.ETag, &d.LastModified, &d.HTTPStatus, &f, &e, &d.IndexedHash, &ix, &task, &d.IndexError, &d.Truncated, &d.NoCache)
	d.FetchedAt = time.Unix(f, 0)
	d.ExpiresAt = time.Unix(e, 0)
	if ix.Valid {
		t := time.Unix(ix.Int64, 0)
		d.IndexedAt = &t
	}
	if task.Valid {
		d.MeiliTaskUID = &task.Int64
	}
	return d, err
}
func CacheFresh(d Document, now time.Time, noCache bool) bool {
	return !noCache && !d.NoCache && now.Unix() < d.ExpiresAt.Unix()
}
