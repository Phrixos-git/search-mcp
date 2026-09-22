---
type: consultation
project: search-mcp
status: open
created: "2026-09-21"
updated: "2026-09-21"
tags: []
---

# deployment設計

## 相談目的

Search MCP、SearXNG、Meilisearch、SQLiteをDocker Compose環境でどう配置・接続・永続化するかをまとめる。

## 現在の構成

```text
Host OS
├─ Discord Bot / Orchestrator
└─ llama.cpp

Docker Compose
│
├─ search-mcp
│    ├─ SearXNG      → http://searxng:8080
│    ├─ Meilisearch  → http://meilisearch:7700
│    └─ SQLite       → /data/search.db
│
├─ searxng
│
└─ meilisearch
```

## Docker Compose実行設定

### Search MCP

- Search MCPもDocker Composeサービスとして起動する。
- listen：`0.0.0.0:<MCP_PORT>`
- MCP Transportの詳細仕様は `search_mcp_server.md` を参照する。
- Orchestratorは初期構成ではHost OS上で動かす。
- Host OSから利用するため、Search MCPのみ`127.0.0.1:<port>`へpublishする。
- LANへ直接公開しない。

### 内部サービス接続

- SearXNG：`http://searxng:8080`
- Meilisearch：`http://meilisearch:7700`
- 固定IPではなくCompose service名を使用する。
- SearXNG / Meilisearchは原則Hostへport公開しない。

### SQLite

- コンテナ内DB Path：`/data/search.db`
- Host：`~/.local/share/ziii`
- `/data`へbind mountする。
- SQLiteはSource of Truthなのでコンテナ削除時も必ず保持する。
- Search MCPは非rootユーザーで動かす。
- mount先ディレクトリへの書き込み権限を保証する。
- WAL利用のためDBファイルだけでなくmount先ディレクトリ自体を書き込み可能にする。

### Meilisearch

- named volumeで永続化する。

### SearXNG

- 設定：bind mount
- cache：named volume

## 起動依存・Healthcheck利用

### 対象サービス

- search-mcp
- searxng
- meilisearch

### Search MCP

- Docker healthcheckでは `search_mcp_server.md` の`/health`を利用する。
- `/ready`は依存サービスを含む利用可能状態確認に使用する。
- Search MCP自身のlivenessと依存先のreadinessを分離する。

### SearXNG / Meilisearch

- SearXNG：軽量なHTTP Endpointでhealthcheckする。
- Meilisearch：公式health endpointを使用する。

### Healthcheck初期値

```text
interval: 10s
timeout: 3s
retries: 3
start_period: 30s
```

### depends_on

Search MCP起動時：

```text
searxng:
    service_healthy

meilisearch:
    service_healthy
```

`depends_on`は起動時制御として使用する。

稼働中の依存サービス障害にはSearch MCP自身のエラー処理・再接続処理で対応する。

## Search MCP環境変数

```text
SEARXNG_BASE_URL=http://searxng:8080
MEILISEARCH_URL=http://meilisearch:7700
SQLITE_DB_PATH=/data/search.db

MCP_HOST=0.0.0.0
MCP_PORT=8081

LOG_LEVEL=info

SQLITE_BUSY_TIMEOUT_MS=5000

SEARXNG_TIMEOUT_MS=18000
FETCH_TIMEOUT_MS=18000
FETCH_MAX_RESPONSE_BYTES=5242880

CACHE_FALLBACK_TTL=24h

MEILI_TASK_POLL_INTERVAL=1s
MEILI_TASK_WATCH_TIMEOUT=30s

SNIPPET_MAX_CHARS=256
```

## Secrets

- Docker Compose Secretsを使用する。
- Git管理しない。
- 対象：
  - `MEILI_MASTER_KEY`
  - その他認証情報
### Search MCP Secret読み込み

- 状態：決定

- Secret受け渡し:
    Docker Compose Secretsを使用
- Secret Path:
    /run/secrets/<secret_name>
- Search MCP設定:
    *_FILE環境変数でSecret file Pathを指定
- Meilisearch:
    MEILI_MASTER_KEY_FILE=/run/secrets/meili_master_key
- 読み込み:
    Search MCP起動時に1回読み込む
- Validation:
    必須Secretが未設定 / 不存在 / 読み込み不可 / 空の場合は起動失敗
- Secret加工:
    末尾のCR/LFのみ除去
- Logging:
    Secret値をログへ出力しない
- 更新:
    Hot Reloadしない
    Secret変更後はSearch MCP再起動で反映
## Docker Image

- `latest`は使用しない。
- Version tagを固定する。
- 必要になればdigest固定へ移行する。

## restart policy

```text
unless-stopped
```

対象：

- search-mcp
- searxng
- meilisearch

## SSRFとの境界

`fetch_page`はDocker private networkを含めprivate IPへのアクセスを拒否する。

ただし以下の内部通信は`fetch_page`とは別Client/経路として扱う。

```text
Search MCP → SearXNG
Search MCP → Meilisearch
```

内部サービス通信と外部Web取得処理を混在させない。

## 関連

- `search_mcp_server.md`
- `search_web.md`
- `fetch_page.md`
- `search_local.md`
