CREATE TABLE documents (
 id INTEGER PRIMARY KEY,
 original_url TEXT NOT NULL,
 canonical_url TEXT NOT NULL UNIQUE,
 title TEXT NOT NULL DEFAULT '',
 content TEXT NOT NULL DEFAULT '',
 content_hash TEXT NOT NULL DEFAULT '',
 content_type TEXT NOT NULL DEFAULT '',
 language TEXT NOT NULL DEFAULT '',
 domain TEXT NOT NULL DEFAULT '',
 etag TEXT NOT NULL DEFAULT '',
 last_modified TEXT NOT NULL DEFAULT '',
 http_status INTEGER NOT NULL DEFAULT 0,
 fetched_at INTEGER NOT NULL DEFAULT 0,
 expires_at INTEGER NOT NULL DEFAULT 0,
 indexed_hash TEXT NOT NULL DEFAULT '',
 indexed_at INTEGER,
 meili_task_uid INTEGER,
 index_error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE INDEX documents_expires_at_idx ON documents(expires_at);
CREATE INDEX documents_content_hash_idx ON documents(content_hash);
