# search-mcp

Go-based stateless MCP service providing `search_web`, `fetch_page`, and `search_local`. SQLite is the source of truth; Meilisearch is a rebuildable search index and SearXNG provides web results.

## MCP tools

Clients use stateless Streamable HTTP at `/mcp` with JSON responses. The service does not run an LLM; the calling application chooses and invokes tools.

| Tool | Input | Behavior |
| --- | --- | --- |
| `search_web` | `query`, optional `max_results` | Queries SearXNG and returns titles, URLs, and snippets. Reports `partial: true` when some engines fail. Search results alone are not saved to the local index. |
| `fetch_page` | `url` | Fetches a public HTTP(S) page, extracts content, and saves it to SQLite with asynchronous Meilisearch synchronization. Returns cache and truncation flags. |
| `search_local` | `query`, optional `max_results` | Searches pages previously saved by `fetch_page` through Meilisearch, returning titles, URLs, snippets, and fetch timestamps. |

Search queries must contain 1–256 characters after trimming. `max_results` defaults to 5 and accepts 1–10. `fetch_page` blocks private, loopback, link-local, and other restricted destinations, including redirects to them. It cannot fetch LAN-only pages through the proxy. Recently fetched pages may take time to appear in `search_local`.

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

Readiness checks SearXNG reachability without querying external search engines; use an actual `search_web` call to verify web search.

## Configuration

Compose reads `.env` for variable substitution; it does not pass every entry to the Search MCP process.

| Compose setting | Default | Purpose |
| --- | --- | --- |
| `SEARXNG_SECRET` | Required | SearXNG secret; set before starting the stack. |
| `SQLITE_DATA_DIR` | `$HOME/.local/share/search-mcp` | Host directory mounted at `/data`; must be writable by UID/GID 10001. |
| `MCP_PORT` | `8081` | Host loopback port and Nginx upstream port; the application container still listens on 8081. |
| `NGINX_PORT` | `38083` | IPv4 listener for the optional Nginx proxy. |
| `LOG_LEVEL` | `info` | Application log level: `debug`, `info`, `warn`, or `error`. |

The Compose file fixes the internal dependency URLs, SQLite path, container listener, and secret-file path. Other process settings in [.env.example](.env.example), such as fetch limits and timeouts, require explicit entries under `search-mcp.environment` to override defaults in Compose. Running the binary directly requires exporting its environment variables; it does not load `.env` itself. Search MCP accepts the Meilisearch key only through `MEILI_MASTER_KEY_FILE` and rejects a direct `MEILI_MASTER_KEY` value.

## LAN access through Nginx

The optional `proxy` profile runs Nginx with Linux host networking so incoming connections can be restricted with UFW. It forwards `/mcp`, `/health`, and `/ready`; other paths return 404. Search MCP remains published on loopback.

Follow [the deployment guide](docs/deployment.md) to restrict access to allowed client IPs before starting the proxy. The target is native Linux Docker Engine. The supplied proxy serves HTTP without TLS or client authentication.

After completing the firewall preparation:

```sh
docker compose --profile proxy config --quiet
docker compose --profile proxy run --rm --no-deps nginx nginx -t
docker compose --profile proxy up --build -d
curl -fsS http://127.0.0.1:38083/ready
```

LAN clients connect to `http://<server-ip>:38083/mcp`. Replace 38083 with your `NGINX_PORT` if changed. Verify access from both an allowed and a disallowed remote host; localhost checks do not validate UFW filtering.

## Stop and inspect

```sh
docker compose logs -f search-mcp
docker compose stop
docker compose down
```

`down` keeps named volumes and the host SQLite directory. `docker compose down -v` deletes Meilisearch and SearXNG volumes; do not use it for routine shutdown. SQLite data remains in the host bind mount unless that directory is removed separately.

If the proxy profile is enabled, include it when stopping or removing the complete stack:

```sh
docker compose --profile proxy logs -f nginx
docker compose --profile proxy stop
docker compose --profile proxy down
```

## Development checks

```sh
gofmt -w ./cmd ./internal
go test ./...
go vet ./...
go build ./...
docker compose --profile proxy config --quiet
```

Unit and integration tests use in-process HTTP fakes and temporary SQLite databases; they do not require running SearXNG or Meilisearch. For a live Compose check, start the stack and call all three MCP tools with an MCP Streamable HTTP client. Web search requires usable SearXNG engines and external network access. `fetch_page` also requires external network access to the requested public site.

The optional proxy integration check requires Python 3, Docker, and the Nginx image:

```sh
python3 scripts/check-proxy.py
```

It validates Compose and Nginx configuration and forwarding against a temporary fake upstream. It does not verify the real MCP tools or remote firewall filtering. See [the deployment guide](docs/deployment.md#automated-proxy-check) for details.

## Full reindex

SQLite is authoritative. To rebuild `pages` from all saved SQLite documents:

```sh
docker compose stop search-mcp
docker compose run --rm search-mcp reindex
docker compose up -d search-mcp
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

## Documentation and local files

Tool and server specifications are in [design/](design/); Linux Nginx/UFW setup and proxy checks are in [docs/deployment.md](docs/deployment.md). The Compose configuration and Nginx template are in [compose.yaml](compose.yaml) and [deploy/nginx/default.conf.template](deploy/nginx/default.conf.template).

Do not commit `.env`, `secrets/`, database/WAL files, logs, or build artifacts.
