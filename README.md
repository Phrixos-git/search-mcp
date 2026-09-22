# search-mcp

Go-based stateless MCP service providing `search_web`, `fetch_page`, and `search_local`. SQLite is the source of truth; Meilisearch is a rebuildable search index and SearXNG provides web results.

## Requirements

- Docker Engine with the Compose plugin
- `openssl` for first-time local secret generation

The Compose file pins application dependencies to versioned image tags. The MCP port is published only on host loopback. Meilisearch and SearXNG are reachable only on the private Compose network.

## First start

```sh
mkdir -p secrets "$HOME/.local/share/search-mcp"
umask 077
openssl rand -hex 32 > secrets/meili_master_key
chmod 0444 secrets/meili_master_key
cp .env.example .env
```

Generate a separate random value for `SEARXNG_SECRET` in `.env` (at least 32 random bytes, for example `openssl rand -hex 32`). `.env` and `secrets/` are ignored by Git. The Search MCP image runs as UID/GID 10001; make the SQLite data directory writable by that user (`sudo chown 10001:10001 "$HOME/.local/share/search-mcp"`) before starting it. If you change `SQLITE_DATA_DIR`, create and grant write permission to that directory as well.

```sh
docker compose config
docker compose up --build -d
docker compose ps
curl -fsS http://127.0.0.1:8081/health
curl -fsS http://127.0.0.1:8081/ready
```

`/health` checks the process database connection. `/ready` checks SQLite, Meilisearch, and SearXNG. MCP clients connect to `http://127.0.0.1:8081/mcp`.

## Stop and inspect

```sh
docker compose logs -f search-mcp
docker compose stop
docker compose down
```

`down` keeps named volumes and the host SQLite directory. `docker compose down -v` deletes Meilisearch and SearXNG volumes; do not use it for routine shutdown. SQLite data remains in the host bind mount unless that directory is removed separately.

## Development checks

```sh
gofmt -w ./cmd ./internal
go test ./...
go build ./...
docker compose config
```

Unit and integration tests use in-process HTTP fakes and temporary SQLite databases; they do not require running SearXNG or Meilisearch. For a live Compose check, start the stack and call all three MCP tools with an MCP Streamable HTTP client. Web search requires usable SearXNG engines and external network access. `fetch_page` also requires external network access to the requested public site.

## Full reindex

SQLite is authoritative. To rebuild `pages` from all saved SQLite documents:

```sh
docker compose run --rm search-mcp reindex
```

The command deletes/recreates the Meilisearch index and repopulates it. It can take time for a large database. Keep the running service stopped while reindexing to avoid concurrent writes to the index.

## Recovery

- **Search MCP unhealthy:** inspect `docker compose logs search-mcp`; verify `/data` permissions and the Meilisearch secret file, then `docker compose restart search-mcp`.
- **`/health` succeeds but `/ready` fails:** inspect `docker compose ps` and the SearXNG/Meilisearch logs. Restart the failed dependency; Compose health-gates initial startup, and Search MCP can be restarted after dependencies become healthy.
- **Meilisearch is empty or its index was lost:** SQLite documents are retained. Restart Search MCP; startup recreates a missing index and resets sync state so SQLite contents are sent again. If the index exists but needs rebuilding, run the full reindex command above.
- **Some documents remain unsynchronized:** inspect application logs and `/ready`, restore Meilisearch, then restart Search MCP to retry pending documents. Sync state advances only after a successful Meilisearch task.
- **SQLite volume/host directory unavailable:** stop the stack, restore the host directory or backup, correct UID 10001 write permissions, and start again. Do not delete SQLite database, WAL, or SHM files while the service is running.
- **Rotate Meilisearch key:** update `secrets/meili_master_key` and restart both Meilisearch and Search MCP. The key configured in Meilisearch and the client file must match.

Container logs are JSON Lines. Debug/info go to stdout; warn/error go to stderr. Secret values and sensitive-keyed attributes are redacted. Operational counters are held in process memory and are not exposed as a public metrics endpoint.

## Package layout

- `cmd/search-mcp`: process startup, startup sync, reindex command, and signal handling
- `internal/mcp`: stateless Streamable HTTP handler and three MCP tools
- `internal/server`: health/readiness and shutdown gate
- `internal/fetch`: URL validation, SSRF-safe HTTP fetching, extraction, and cache
- `internal/storage`: SQLite connection, migrations, transactions, and documents
- `internal/indexer`: Meilisearch index settings, tasks, differential sync, and reindex
- `internal/search`: SearXNG client and local search
- `internal/config`, `internal/requestctx`, `internal/toolerror`, `internal/logging`, `internal/metrics`: configuration, correlation IDs, error mapping, structured logs, and internal counters

## Repository status

Phase 1–5 implementation is present. The configured remote `origin` points to [`Phrixos-git/search-mcp`](https://github.com/Phrixos-git/search-mcp). Local `main` and GitHub `main` were verified at commit `5a9833543b6f8ce28a032fa508b5e6240efc1df0`; push subsequent commits to `origin main`. Do not commit `.env`, `secrets/`, database/WAL files, logs, or build artifacts.
