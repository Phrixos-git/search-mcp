---
type: consultation
project: search-mcp
status: open
created: "2026-09-21"
updated: "2026-09-21"
tags: []
---

# search_local

## 相談目的

Search MCPを構成する3 Toolsのうち、`search_local` の実装時とその後の拡張で必要な最低限の設計項目を決定する。

## 現在の構成

```text
search_local

Input
├─ query
└─ max_results

Output
└─ results[]
    ├─ title
    ├─ url
    ├─ snippet
    └─ fetched_at

+ 0件ポリシー
+ Timeout
+ 鮮度の扱い
+ Ranking
+ 結果サイズ上限
+ 検索対象Index

共通Error:
    search_mcp_server.md を参照
```

## 検討事項

### SL-01：query

- 状態：決定
- 前提・制約：`search_web`の設定を流用
- 決定内容：
  - 型：string
  - 必須：Yes
  - 空文字：禁止
  - 最大文字数：256文字
  - 文字数判定：Unicode文字単位
  - 前後空白：trimしてから判定
- 影響する項目：Meilisearch / Orchestrator

### SL-02：max_results

- 状態：決定
- 前提・制約：`search_web`の設定を流用
- 決定内容：
  - デフォルト値：5件
  - 上限値：10件

### SL-03：検索結果の基本構造

- 状態：決定
- 前提・制約：JSON、`search_web`の設定を流用
- 決定内容：
  - `title`：必須
  - `url`：必須
  - `snippet`：空を許容
  - `fetched_at`：必須

### SL-04：共通Errorの扱い

- 状態：共通設計へ移管
- 決定内容：
  - Error SchemaおよびError Code別のリトライ方針は `search_mcp_server.md` を参照する。

### SL-05：0件時の扱い

- 状態：決定
- 決定内容：
  - エラーではなく正常応答とする。
- 残課題：
  - 0件応答時に質問者へどう返すかはOrchestrator/LLM側で決める。

### SL-06：ローカル情報の鮮度の扱い

- 状態：決定
- 前提・制約：`fetched_at`はRFC 3339
- 決定内容：
  - `fetched_at`を返してOrchestrator/LLMに判断させる。
  - HITした`fetched_at`が2週間以上前なら再度Web検索するよう促す。

### SL-07：検索結果の順序

- 状態：決定
- 決定内容：
  - Meilisearchのランキング順を採用する。

### SL-08：検索結果サイズの上限

- 状態：決定
- 決定内容：
  - `snippet`最大長：256文字
  - UTF-8として有効な文字単位で切る。
  - 超過分は切り捨てる。
  - `snippet`自体が存在しない場合は空を許容する。

### SL-09：Meilisearch問い合わせTimeout

- 状態：決定
- 決定内容：
  - Timeout：10秒

### SL-10：検索対象Index

- 状態：決定
- 決定内容：
  - Meilisearchは単一`pages` Indexを使用する。
  - LLMからIndex名は指定させない。
  - `search_local`は常に`pages`を検索する。
  - 文書の言語はSQLiteおよびMeilisearchの`language`属性で管理する。
  - 言語による絞り込みが必要な場合はfilterで対応する。
  - 将来必要になればIndex分割を検討する。

## 関連

- `search_web.md`
- `fetch_page.md`
- `search_mcp_server.md`
- `deployment.md`
