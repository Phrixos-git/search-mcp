---
type: consultation
project: search-mcp
status: open
created: "2026-09-21"
updated: "2026-09-21"
tags: []
---

# fetch_page

## 相談目的

Search MCPを構成する3 Toolsのうち、`fetch_page` の実装時とその後の拡張で必要な最低限の設計項目を決定する。

## 現在の構成

```text
fetch_page

Input
└─ url(original_url)

Output
├─ title
├─ url(final_url)
├─ content
├─ fetched_at
├─ from_cache
└─ truncated

+ キャッシュ
+ Timeout
+ 本文サイズ上限
+ URL正規化
+ SSRF対策
+ Redirect
+ HTTPレスポンスサイズ上限
+ Content-Type制限
+ 本文抽出
+ HTTPキャッシュ再検証
+ SQLite / Meilisearch同期
+ HTTP Status処理

共通Error:
    search_mcp_server.md を参照
```

## 検討事項

### FP-01：url

- 状態：決定
- 決定内容：
  - 型：string
  - 必須：Yes
  - 空文字：禁止
  - 許可scheme：`http` / `https`
  - 最大長：UTF-8で最大2048bytes
  - byte数チェックはプログラム側で実施する。

### FP-02：取得結果の基本構造

- 状態：決定
- 前提・制約：JSON、`fetched_at`はRFC 3339
- 決定内容：
  - `title`
  - `url`
  - `content`
  - `fetched_at`
  - `from_cache`
  - `truncated`

### FP-03：共通Errorの扱い

- 状態：共通設計へ移管
- 決定内容：
  - Error SchemaおよびError Code別のリトライ方針は `search_mcp_server.md` を参照する。
  - HTTP Statusから共通Errorへの変換はFP-15で扱う。
  - `fetch_page`固有ErrorはFP-17で扱う。

### FP-04：キャッシュ利用

- 状態：決定
- 決定内容：
  - SQLiteに有効な保存済みページがある場合は有効キャッシュを優先する。

### FP-05：HTTP取得時のTimeout

- 状態：決定
- 決定内容：
  - Timeout：18秒

### FP-06：本文サイズ上限

- 状態：決定
- 決定内容：
  - `content`最大：8,192 tokens
  - 上限超過時：切り捨て
  - `truncated: true / false`を必ず返す。
  - 元本文サイズは可能なら`original_length`として保持する。
  - 自動で追加取得はしない。
  - 1リクエスト全体では別途24K tokens程度のTool Context上限をOrchestrator側に設ける。

### FP-07：URL正規化

- 状態：決定
- 決定内容：
  - `original_url`：入力値を保持
  - `canonical_url`：
    - scheme lowercase
    - host lowercase
    - default port削除
    - fragment削除
    - path正規化
    - tracking parameter削除
  - 変更しない：
    - `http` ↔ `https`
    - `www`有無
    - 末尾 `/`
    - 通常のquery parameter
  - `canonical_url`はSQLiteでUNIQUE。
  - `search_web`と`fetch_page`で同じURL正規化関数を使用する。

### FP-08：SSRF対策

- 状態：決定
- 決定内容：
  - 許可：`http` / `https`
  - 拒否：
    - localhost
    - loopback
    - private IP
    - link-local
    - metadata endpoint
    - multicast
    - unspecified address
  - DNS名前解決後IPを必ず検査する。
  - Redirectごとに再検査する。
  - `fetch_page`では内部ネットワークアクセスを禁止する。
  - SSRF拒否時はFP-17の固有Errorを返す。

### FP-09：Redirectの扱い

- 状態：決定
- 前提・制約：FP-08のURL/IP検証
- 決定内容：
  - Redirectを許可する。
  - 最大回数：1回
  - Redirect先のURL/IP検証：必須
  - 入力URL：`original_url`
  - Redirect完了後URL：`final_url`
  - `final_url`を正規化したURL：`canonical_url`

### FP-10：最大HTTPレスポンスサイズ

- 状態：決定
- 決定内容：
  - 最大サイズ：5 MiB
  - 対象：展開後Response Body
  - 上限超過：取得失敗
  - 部分データはSQLiteへ保存しない。
  - Meilisearchにも登録しない。
  - `Content-Length`が上限超過なら読み込み前に拒否する。
  - 読み込み中も実サイズを監視する。
  - Error：`response_too_large`

### FP-11：許可するContent-Type

- 状態：決定
- 決定内容：
  - `text/html`
  - `text/plain`

### FP-12：本文抽出方式

- 状態：決定
- 決定内容：
  - 本文抽出ライブラリ：既存ライブラリを使用
  - 常時除去：
    - script
    - style
    - noscript
    - iframe
    - canvas
    - svg
    - form
    - input
    - button
  - Navigation / Header / Footer：除去
  - リンク：リンク本文のみ残す
  - 画像：altのみ残す
  - table / code / pre：Markdownへ変換
  - 出力：軽量Markdown
  - 抽出失敗時：bodyから簡易抽出する

### FP-13：HTTPキャッシュ再検証

- 状態：決定
- 決定内容：
  - ETagを保存・利用する。
  - Last-Modifiedも保存・利用する。
  - ETagがあれば`If-None-Match`を優先する。
  - ETagがなくLast-Modifiedがあれば`If-Modified-Since`を使用する。
  - 304 Not Modifiedなら保存済み本文を再利用する。
  - 200 OKなら本文・ETag・Last-Modifiedを更新する。
  - 両方存在しない場合は通常GETする。

### FP-14：永続化と応答処理の分離

- 状態：決定
- 決定内容：
  - 本文抽出完了後、SQLite保存までは同期処理とする。
  - SQLite commit成功後に`fetch_page`結果を返せる。
  - Meilisearch登録は応答系と分離する。
  - Meilisearch登録失敗は`fetch_page`自体の失敗にはしない。
  - 未同期状態は`indexed_hash`等で管理する。
  - 後から差分同期できるようにする。

```text
HTTP取得
  ↓
本文抽出
  ↓
content確定
  ↓
SQLite保存
  ↓ commit成功
  │
  ├──────────────→ fetch_page結果を返す
  │
  └──────────────→ Meilisearch登録
                       ↓
                    Task確認
                       ↓
                  indexed_hash更新
```

### FP-15：取得失敗時のHTTP Statusの扱い

- 状態：決定
- 決定内容：
  - `invalid_argument`
    - 不正URL
    - 未対応scheme
    - 必須値不足
  - `not_found`
    - 404
    - 410
  - `access_denied`
    - 401
    - 403
    - 451
  - `rate_limited`
    - 429
    - `retry_after_seconds`ありならその値を優先、なければ5秒
  - `timeout`
    - 接続Timeout
    - Read Timeout
    - HTTP 408
  - `upstream_error`
    - 500
    - 502
    - 503
    - 504
  - `internal_error`
    - MCP内部処理失敗
    - SQLite障害
    - 想定外例外

### FP-16：キャッシュ有効期限判定

- 状態：決定
- 決定内容：
  - 有効：`now < expires_at`
  - 期限切れ：`now >= expires_at`
  - `expires_at`決定順：
    1. `Cache-Control: max-age`
    2. `Expires`
    3. fallback TTL
  - fallback TTL：24時間
  - `no-cache`：保存可能、再利用前に必ず再検証
  - `no-store`：保存しない
  - 期限切れ：FP-13へ
  - 304：本文再利用、`expires_at`更新
  - 判定不能：期限切れ扱い

### FP-17：fetch_page固有Error

- 状態：決定
- 決定内容：
  - `blocked_address`
  - `blocked_redirect`
  - `response_too_large`
  - `unsupported_content_type`

## 関連

- `search_web.md`
- `search_local.md`
- `search_mcp_server.md`
- `deployment.md`
