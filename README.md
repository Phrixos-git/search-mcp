# search-mcp

Go製Search MCPの基盤リポジトリです。設計書に従い、以下のToolを提供する構成を段階的に実装します。

- `search_web`
- `fetch_page`
- `search_local`

## Implemented phases

- Phase 1: Go project, configuration, Docker Secret loading, request/tool-call context.
- Phase 2: SQLite source of truth, URL canonicalization, and HTTP cache metadata.
- Phase 3: page fetching, SSRF-safe HTTP access, Meilisearch task synchronization, startup differential sync, and full reindex components.

MCP transport and tool registration are still pending.

## Configuration

設定例は [.env.example](.env.example) を参照してください。Meilisearchの認証情報は環境変数へ直接設定せず、`MEILI_MASTER_KEY_FILE`で指定したDocker Secretファイルから読み込みます。Secret値やruntime dataをリポジトリへ保存しないでください。

## Development

```text
go test ./...
go build ./...
```

Go module pathはGitHub repository情報が確定していないため、現時点ではローカル用の`search-mcp`としています。remote追加・GitHub pushはowner、repository名、公開範囲、認証が確定した後に行います。

## Repository layout

```text
cmd/search-mcp/       executable entrypoint
internal/config/      environment and Docker Secret configuration
internal/requestctx/  request and tool-call context metadata
internal/{fetch,logging,mcp,search,server,storage}/ package boundaries
migrations/           SQLite migration files
design/               implementation source documents
```
