# ローカルLLM向け検索MCP 設計要約

## 1. 目的

Discord Bot から `llama.cpp` のローカルLLM APIを呼び出し、LLMが必要と判断した場合に検索MCPを利用して情報を取得・整理する。

目指す挙動は以下。

- 質問に直接回答する
- 必要な場合のみ検索する
- 既存のローカル情報を優先して再利用する
- 必要ならWeb検索へフォールバックする
- 検索結果を要約・比較する
- 問題解決や意思決定の場面では、次の行動や判断基準も提示する
- 実際のファイル変更・コマンド実行などは当面自動化しない

---

## 2. 全体構成

```text
                  Discord
                     │
                     ▼
          ┌────────────────────┐
          │ Discord Bot        │
          │                    │
          │ Discord Adapter    │
          │        │           │
          │        ▼           │
          │ Orchestrator       │
          │ ├─ Session Manager │
          │ ├─ Request Queue   │
          │ ├─ LLM Client      │
          │ └─ MCP Client      │
          └───┬────────────┬───┘
              │            │
              │            │ MCP
              │            ▼
              │    ┌────────────────┐
              │    │ Go Search MCP  │
              │    ├────────────────┤
              │    │ search_web     │
              │    │ fetch_page     │
              │    │ search_local   │
              │    └──┬────────┬────┘
              │       │        │
              │       ▼        ▼
              │    SearXNG  Meilisearch
              │                 ▲
              │                 │ index
              │              SQLite
              │                 ▲
              │                 │
              │          fetch_pageで保存
              │
              │ OpenAI互換API
              ▼
        ┌──────────────┐
        │ llama.cpp    │
        │ llama-server │
        │ Local LLM    │
        └──────────────┘
```

### 重要な責務分担

`Local LLM → MCP` と直接つなぐのではなく、以下の構成にする。

```text
Discord Bot / Orchestrator
├─ llama.cpp API Client
└─ MCP Client
```

LLMは「どのToolを使いたいか」を判断し、実際のTool Call実行や上限管理はOrchestratorが担当する。

---

## 3. 採用ミドルウェア

| コンポーネント | 用途 | 採用理由 |
|---|---|---|
| Go | Search MCP実装 | 軽量、単一バイナリ化しやすい |
| llama.cpp | ローカルLLM API | 既存のローカルLLM実行基盤 |
| SearXNG | Web検索 | 外部有料検索APIへの依存を避けられる |
| Meilisearch | ローカル全文検索 | 高速で導入が比較的容易 |
| SQLite | 正本DB・取得情報管理 | 軽量、サーバ不要、再構築元として使える |
| Docker / Docker Compose | SearXNG等の管理 | 環境分離と更新が容易 |
| Go HTTP Client | Webページ取得 | 追加ミドルウェア不要 |
| HTML本文抽出ライブラリ | Web本文抽出 | LLMへ不要なHTMLを渡さない |

### 初期段階では導入しないもの

- Qdrant
- Embeddingモデル
- Playwright
- Redis
- Caddy / nginx

必要性が出た段階で追加する。

---

## 4. Search MCP のTool

初期Toolは3つ。

### `search_web`

SearXNGを利用してWeb検索する。

想定入力:

```text
query
max_results
language
time_range
```

主な返却情報:

```text
title
url
snippet
engine
```

---

### `fetch_page`

指定URLを取得し本文抽出する。

主な役割:

1. URL正規化
2. SQLiteキャッシュ確認
3. 必要ならHTTP取得
4. HTML本文抽出
5. SQLite保存
6. Meilisearch更新

---

### `search_local`

Meilisearchから保存済みページを全文検索する。

基本方針:

```text
search_local
    ↓
不足している / 古い
    ↓
search_web
    ↓
必要なURLだけfetch_page
```

Local-firstを基本にする。

---

## 5. SQLite の役割

SQLiteは単なる一時キャッシュではなく、検索システムの **Source of Truth（正本）** とする。

```text
SQLite
   ↓
Meilisearchを再生成可能
```

Meilisearchは検索用インデックスとして扱い、障害時にSQLiteからFull Reindexできる構成にする。

---

## 6. SQLiteのキャッシュ方針

Redis等の追加キャッシュ層は初期段階では不要。

ただし、取得済みWebページをSQLiteへ保存して再取得を抑制するHTTPキャッシュは必要。

```text
fetch_page(URL)
      │
      ▼
SQLite確認
      │
      ├─ 有効なキャッシュあり
      │       └─ SQLiteから返却
      │
      └─ 期限切れ / 未登録
              ↓
           HTTP取得
```

---

## 7. SQLite 推奨スキーマ案

```text
documents
---------
id                  INTEGER PRIMARY KEY
original_url        TEXT
canonical_url       TEXT UNIQUE NOT NULL
title               TEXT
content             TEXT
content_hash        TEXT
content_type        TEXT
language            TEXT

domain              TEXT
etag                TEXT
last_modified       TEXT

http_status         INTEGER
fetched_at          INTEGER
expires_at          INTEGER

indexed_hash        TEXT
indexed_at          INTEGER
meili_task_uid      INTEGER
index_error         TEXT

created_at          INTEGER
updated_at          INTEGER
```

### 主なカラムの意味

| カラム | 用途 |
|---|---|
| `original_url` | ユーザー/検索結果から得た元URL |
| `canonical_url` | 重複判定用の正規化済みURL |
| `content_hash` | 本文変更判定 |
| `etag` | HTTP条件付きリクエスト |
| `last_modified` | HTTP条件付きリクエスト |
| `fetched_at` | 最終取得時刻 |
| `expires_at` | 自前キャッシュ期限 |
| `language` | ja / en / unknown |
| `indexed_hash` | Meilisearch同期状態判定 |
| `meili_task_uid` | Meilisearch非同期Task追跡 |

### 最低限のIndex

```text
UNIQUE(canonical_url)
INDEX(expires_at)
INDEX(content_hash)
```

`content`にはSQLiteのIndexを張らない。全文検索はMeilisearchに任せる。

---

## 8. SQLite パフォーマンス設定

初期推奨:

```text
journal_mode = WAL
synchronous = NORMAL
busy_timeout = 3000～5000ms
foreign_keys = ON

cache_size = default
wal_autocheckpoint = default
```

### 方針

- 最初から `cache_size` を大きくしない
- `wal_autocheckpoint` も初期値で開始
- インデックスを増やしすぎない
- 書き込みTransactionは短くする
- 必要に応じて `PRAGMA optimize` を定期実行

---

## 9. URL正規化

目的は「同じページを別URLとして重複保存しない」こと。

### 実施する正規化

比較的安全なものだけ行う。

```text
schemeを小文字化
hostnameを小文字化
fragment (#xxx) を削除
http:80 を削除
https:443 を削除
./ ../ のpath正規化
percent-encodingの標準化
明確なtracking parameterを削除
```

tracking parameter例:

```text
utm_source
utm_medium
utm_campaign
fbclid
gclid
```

### 原則やらない正規化

サイトによって別ページになる可能性があるため、以下は安易に行わない。

```text
http → https 強制変換
wwwの除去
末尾 / の一律削除
query parameterの全削除
query parameter順序の無条件変更
```

方針は **保守的なURL正規化**。

---

## 10. HTTPキャッシュ期限

優先順位:

```text
1. Cache-Control
2. Expires
3. Search MCP側のfallback TTL
```

### 条件付きリクエスト

SQLiteへ以下を保存する。

```text
ETag
Last-Modified
```

再取得時:

```text
If-None-Match
If-Modified-Since
```

を利用する。

`304 Not Modified` の場合は本文を再保存しない。

### fallback TTL

サーバ側にキャッシュ指定がない場合の初期案:

```text
通常ページ        24時間
技術ドキュメント  3～7日
更新頻度が高い頁   6～24時間
404                数時間～1日
```

初期実装は単純化して、

```text
fallback TTL = 24時間
```

から始めてもよい。

---

## 11. SQLite → Meilisearch 同期

必ず以下の関係にする。

```text
SQLite = Source of Truth
Meilisearch = 再生成可能な検索Index
```

### 通常同期

```text
fetch_page
 ↓
content_hash計算
 ↓
SQLite更新
 ↓
Meilisearch登録
 ↓
Meilisearch Task成功確認
 ↓
indexed_hash = content_hash
```

### 重要

MeilisearchのDocument登録は非同期Task。

```text
API呼び出し
 ↓
taskUid取得
 ↓
enqueued
 ↓
processing
 ↓
succeeded
```

`indexed_hash`は **APIリクエスト送信時ではなく、Taskが`succeeded`になった時点で更新する**。

### 差分同期

```text
content_hash != indexed_hash
```

または

```text
indexed_hash IS NULL
```

を対象に再indexする。

### Full Reindex

障害復旧用に必ず用意する。

```text
Meilisearch index削除
 ↓
index再作成
 ↓
SQLite全件取得
 ↓
Meilisearch再登録
```

---

## 12. Meilisearch 初期設定

基本設定はデフォルトを活かす。

初期段階でCPU/RAM/Thread等を細かく調整しない。

### Primary Key

SQLiteの `id` をそのまま利用。

```text
SQLite
id = 123

↓ 同一ID

Meilisearch
id = 123
```

URLをPrimary Keyにはしない。

---

### searchableAttributes

デフォルトの全フィールド検索ではなく明示する。

```text
title
content
```

`title`を先にする。

---

### filterableAttributes

必要なものだけ指定。

```text
language
domain
content_type
```

---

### sortableAttributes

初期候補:

```text
fetched_at
updated_at
```

---

### Ranking

初期設定のまま開始。

---

### Typo Tolerance

初期設定のまま開始。

ただし以下のような技術用語が多いため、将来的に調整対象になる可能性あり。

```text
llama.cpp
ROCm
Qwen
GGUF
--ctx-size
PRAGMA
systemd
```

検索ノイズが問題になった段階で変更する。

---

## 13. 日本語 / 英語

SQLiteには最初から言語情報を保存する。

```text
language =
    ja
    en
    unknown
```

### Meilisearch

初期段階:

```text
pages
```

という単一index。

まずは構成を単純にする。

### 将来

データ量増加や検索精度低下が発生した場合:

```text
pages_ja
pages_en
```

に分割する。

`search_local`側で言語に応じたindexを検索する。

---

## 14. Discord Bot / Orchestrator

Search MCP側はDiscordを意識しない。

Discord固有処理はBot層へ閉じ込める。

```text
Discord Adapter
      │
      ▼
Orchestrator
   ├─ LLM Client
   └─ MCP Client
```

これにより将来、

```text
CLI
Web UI
Slack
他アプリ
```

からも同じLLM/Search MCPを利用可能にする。

---

## 15. 会話管理

Discordでは会話の混線防止が必要。

候補キー:

```text
guild_id
channel_id
thread_id
user_id
```

例:

```text
conversation_key =
guild_id + channel_id + thread_id
```

検索用SQLiteと会話履歴DBは分離する。

```text
search.db
    └─ 検索MCP用

bot.db
    └─ 会話履歴 / Session管理用
```

ただし初期段階では会話履歴をメモリ上で管理してもよい。

---

## 16. 同時実行制御

llama.cppが `--parallel 1` の場合、複数Discordリクエストを同時生成させない。

OrchestratorにRequest Queueを持たせる。

```text
Request A ─┐
Request B ─┼─ Queue → llama.cpp
Request C ─┘
```

検索自体はある程度並列化可能。

---

## 17. Tool Call制御

「何を検索するか」はLLMに判断させる。

「どこまで検索を許可するか」はOrchestratorで制御する。

例:

```text
1リクエスト最大Tool Call: 10回

search_web:
最大3回

fetch_page:
最大5ページ

search_local:
最大3回
```

目的:

- 検索ループ防止
- SearXNG過剰アクセス防止
- レスポンス時間制御
- llama.cppへの不要な再推論防止

---

## 18. Timeout

初期目安:

```text
SearXNG        10秒
fetch_page     10～20秒
Meilisearch     3～5秒
MCP Tool全体   30秒
```

値は運用しながら調整する。

---

## 19. LLMに期待する動作

LLMには単なる検索結果要約ではなく、以下の4つの役割を持たせる。

```text
Answer
    質問へ直接回答

Research
    必要な場合のみ検索

Synthesis
    複数の情報を整理・比較

Guidance
    次の行動や判断材料を提示
```

### 基本フロー

```text
Discordで質問
   ↓
LLMが質問を理解
   │
   ├─ 検索不要
   │     ↓
   │   そのまま回答
   │
   └─ 情報不足 / 最新情報が必要
         ↓
      search_local
         ↓
      必要なら search_web
         ↓
      必要URLを fetch_page
         ↓
      検索結果をLLMへ戻す
         ↓
      情報を整理・比較
         ↓
      最終回答
      ＋
      必要なら次の行動
      ＋
      判断基準
```

### Guidanceの出し方

毎回「次のステップ」を強制しない。

以下の場合のみ有用な次の行動を提示する。

- トラブルシューティング
- 技術調査
- 比較
- 意思決定
- 設計相談
- 作業手順を考える必要がある場合

例:

```text
原因としてAが最も可能性が高い。
まずXを確認する。

XがYなら設定A、
XがZなら設定Bを確認する。
```

---

## 20. ストレージ

現状の空き容量約700GBで、初期構成には十分余裕がある。

初期段階では以下を中心に保存する。

```text
抽出済みWeb本文
メタデータ
SQLite DB
Meilisearch Index
ログ
```

容量増加を防ぐため、

- 画像・動画を原則保存しない
- 元HTMLは必要性がなければ保存しない
- キャッシュ期限を設ける
- 古いキャッシュ削除を将来的に検討する

本格的なEmbedding/RAG導入までは大容量にはなりにくい。

---

# 21. 初期構成の確定案

```text
Local LLM:
    llama.cpp

Application:
    Discord Bot
    Orchestrator

MCP:
    Go Search MCP

Search MCP Tools:
    search_web
    fetch_page
    search_local

Web Search:
    SearXNG

Local Search:
    Meilisearch

Source of Truth:
    SQLite

Cache:
    SQLiteによるHTTPキャッシュ

URL:
    保守的な正規化

Language:
    ja / en / unknown
    Meilisearchは最初1 index

Sync:
    content_hash / indexed_hash
    差分同期
    Full Reindex

LLM policy:
    Local-first
    必要時のみWeb検索
    回答 + 必要なら次の行動/判断材料
```

---

# 22. 次のチャットで詰めるべき項目

以下を次の設計フェーズで決める。

## Search MCP API設計

- `search_web` の厳密なInput / Output schema
- `fetch_page` のInput / Output schema
- `search_local` のInput / Output schema
- エラー形式
- Tool Callの最大結果件数
- LLMへ返す本文サイズ

## SQLite設計

- 最終的なDDL
- URL正規化関数の仕様
- TTL計算ロジック
- HTTP Cache-Control解釈範囲
- DB migration方法
- 古いキャッシュ削除方法

## Meilisearch設計

- Index schema
- searchableAttributes
- filterableAttributes
- sortableAttributes
- ranking / typo toleranceの初期値
- Task状態管理
- Full Reindexコマンド

## fetch_page設計

- HTTP Client設定
- User-Agent
- redirect上限
- Response Size上限
- Content-Type制限
- HTML本文抽出方式
- PDF対応の要否
- robots.txtの扱い
- SSRF対策
- private/local addressへのアクセス制限

## Orchestrator設計

- llama.cpp Tool Callingループ
- `search_local → search_web → fetch_page` の判断ルール
- Tool Call回数制限
- Timeout
- Request Queue
- Conversation管理
- 検索結果をLLMへ戻す際のContext管理

## 運用

- SearXNGの検索エンジン選択
- ログ
- metrics
- SQLite Backup
- Meilisearch Backup / Reindex
- ディスク容量監視
