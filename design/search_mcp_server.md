---
type: consultation
project: search-mcp
status: open
created: "2026-09-21"
updated: "2026-09-21"
tags: []
---

# Search MCP Server 共通設計

## 相談目的

`search_web` / `fetch_page` / `search_local` に共通するSearch MCP Server自身の仕様を集約する。

## MCP Transport

- 状態：決定
- Transport：Streamable HTTP
- 初期構成：Stateless

Tool個別仕様は以下を参照する。

- `search_web.md`
- `fetch_page.md`
- `search_local.md`

Docker上でのlisten / publish方法は `deployment.md` を参照する。

## 共通Error

### Error Schema

```text
Error
├─ code
├─ message
├─ retryable
├─ http_status          optional
├─ retry_after_seconds  optional
└─ error_id             optional
```

### Error Code

```text
invalid_argument
not_found
access_denied
rate_limited
timeout
upstream_error
network_error
internal_error
```

`network_error`の例：

```text
DNS解決失敗
Connection refused
Connection reset
TLS handshake失敗
```

`internal_error`では必要に応じて`error_id`を返し、内部詳細はサーバーログ側で追跡する。

### リトライ方針

| Error Code         | retryable | 基本対応                               |
| ------------------ | --------: | ---------------------------------- |
| `invalid_argument` |     false | 入力修正                               |
| `not_found`        |     false | 同一URLを再試行しない                       |
| `access_denied`    |     false | 同一URLを再試行しない                       |
| `rate_limited`     |      true | 回数制限付き。`Retry-After`相当があれば優先       |
| `timeout`          |      true | 回数制限付き                             |
| `upstream_error`   |      true | 回数制限付き                             |
| `network_error`    |  原因によって決定 | DNS/TCP/TLS等の原因に応じてtrue/falseを必ず返す |
| `internal_error`   |     false | MCP内部障害として扱う                       |

- リトライ上限：3回
- 基本リトライ間隔：5秒
- Tool固有Errorは各Tool設計書で定義する。
- network_error:
	- 一時的DNS障害 → true
	- Connection reset → true
	- Connection refused → true
	- TLS証明書検証失敗 → false

## /health

- 状態：決定
- Method：`GET`
- Path：`/health`
- 用途：Search MCP自身のliveness確認
- 確認対象：
  - Search MCPプロセス自身
  - SQLiteがopen済み
  - SQLiteで`SELECT 1`が成功すること
- 確認対象外：
  - SearXNG
  - Meilisearch

基本応答：

```text
正常:
    HTTP 200

異常:
    HTTP 503
```

依存サービス停止だけではSearch MCP自身をunhealthy扱いにしない。

Docker healthcheckからの利用方法は `deployment.md` を参照する。

## /ready

- 状態：決定
- Method：`GET`
- Path：`/ready`
- 用途：検索システム全体のreadiness確認
- 確認対象：
  - `/health`相当の確認
  - SearXNG到達可能
  - Meilisearch到達可能

基本応答：

```text
利用可能:
    HTTP 200

依存サービスを含め利用不可:
    HTTP 503
```

Meilisearch停止時でもSearch MCPプロセス自体はaliveとし、`/ready`のみ失敗させる。

## Logging

- 状態：決定
- 現時点で決まっていること：
  - `LOG_LEVEL`を環境変数から設定可能にする。
  - 初期値は`info`。
- 決定内容：
  - Log形式：JSON Lines
  - 出力先：stdout,stderr
	  - info / debug → stdout
	  - warn / error → stderr
  - Request / Tool Call単位の識別子：
	  - request_id
		- 1ユーザーリクエストを識別 
		- 原則Orchestratorが生成 
		- 未指定時はSearch MCPが生成
	  - tool_call_id
		  - 個別Tool Callを識別
		  - Search MCPがTool Callごとに生成
- 共通ログ項目 
	- time 
	- level 
	- event 
	- message 
	- request_id 
	- tool_call_id optional 
	- tool optional 
	- duration_ms optional
  - Errorの記録項目：
	  - error_id
	  - error_code
	  - error_message
	  - request_id
	  - tool_call_id
	  - tool
	  - operation
	  - retryable
	  - http_status
	  - duration_ms
	  - upstream
	  - attempt
  - Log rotation：
	  - Search MCPでは実施しない 
	  - Docker logging driver側で管理する
## Metrics / Monitoring

- 状態：決定
### 初期Metrics

- Tool Call回数
- Tool成功 / 失敗回数
- Tool処理時間
- Error Code別件数
- Timeout回数
- Cache hit / miss
- Meilisearch未同期件数
- Meilisearch同期失敗件数
- SearXNG / Meilisearch状態
### Monitoring

- `/health`でSearch MCP自身を監視
- `/ready`で依存サービスを含めた状態を監視
- 初期段階では外部監視基盤を必須としない
- 将来必要になればPrometheus `/metrics`を追加する
### Metrics属性

使用(監視目的):
- tool
- status
- error_code
- upstream

使用しない:
- request_id
- tool_call_id
- error_id
- URL

### 初期段階:

    Metrics公開機能は実装しない
    監視対象項目のみ定義する
### 将来:

    /metricsを追加してPrometheus形式で公開
## Shutdown

- 状態：決定
### SD-01：Shutdownトリガー
- SIGTERM
- SIGINT
### SD-02：新規Request受付
- Shutdown開始後は停止
### SD-03：処理中Request
- Graceful Shutdown
- 最大待機時間：30秒
- 超過時はContext cancel
### SD-04：SQLite
- Transaction完了を待つ
- 新規処理を開始しない
- Connection Poolをclose
### SD-05：Meilisearch同期
- 新規Task生成を停止
- 実行中Task監視は可能な範囲で完了待ち
- 未完了状態はSQLiteへ残し、次回起動時に再同期
### SD-06：外部HTTP
- Shutdown Contextを伝播
- Timeout超過時にcancel
### SD-07：終了順序概要
- readiness停止
- HTTP受付停止
- 実行中処理待機
- Background処理停止
- DB close
- Logger flush
- Process終了
### SD-07-01：終了順序フロー
```text
SIGTERM / SIGINT
    ↓
shutting_down=true
    ↓
/ready → 503
    ↓
新規Tool Requestを拒否
    ↓
HTTP Server Graceful Shutdown開始
    ├─ 新規HTTP接続を停止
    └─ 処理中Request完了待ち
    ↓
Background処理停止
    ↓
DB close
    ↓
Logger flush
    ↓
Process終了
```
### SD-08：Shutdown Log
- shutdown_started
- shutdown_completed
- shutdown_timeout
### SD-09：強制終了
- Graceful Timeout超過後はcancelして終了
## 関連

- `search_web.md`
- `fetch_page.md`
- `search_local.md`
- `deployment.md`
