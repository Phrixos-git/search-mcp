---
type: consultation
project: search-mcp
status: fix
created: "2026-09-20"
updated: "2026-09-21"
tags: []
---

# search_web

## 相談目的

Search MCPを構成する3 Toolsのうち、`search_web` の実装時とその後の拡張で必要な最低限の設計項目を決定する。

## 現在の構成

```text
search_web

Input
├─ query
└─ max_results

Output
└─ results[]
    ├─ title
    ├─ url
    └─ snippet

+ Timeout
+ 重複除去
+ 部分失敗ポリシー

共通Error:
    search_mcp_server.md を参照
```

## 検討事項

### SW-01：query

- 状態：決定
- 決めたいこと：空文字の取り扱い、最大文字数
- 前提・制約：特になし
- 決定内容：
  - 型：string
  - 必須：Yes
  - 空文字：禁止
  - 最大文字数：256文字
  - 文字数判定：Unicode文字単位
  - 前後空白：trimしてから判定
- 影響する項目：SearXNG
- 残課題：なし

### SW-02：max_results

- 状態：決定
- 決めたいこと：LLMへ返す最大検索件数
- 決定内容：
  - デフォルト値：5件
  - 上限値：10件

### SW-03：検索結果の基本構造

- 状態：決定
- 決めたいこと：検索結果の受け取り方
- 前提・制約：JSON
- 決定内容：
  - `title`：必須
  - `url`：必須
  - `snippet`：空を許容

### SW-04：共通Errorの扱い

- 状態：共通設計へ移管
- 決定内容：
  - Error SchemaおよびError Code別のリトライ方針は `search_mcp_server.md` を参照する。
  - `search_web` 固有の上流障害判定のみ本設計書で扱う。

### SW-05：0件時の扱い

- 状態：決定
- 決定内容：
  - エラーではなく正常応答とする。
  - 全Engine正常応答で0件の場合は空の結果を返す。
- 残課題：
  - 0件応答時に質問者へどう返すかはOrchestrator/LLM側で決める。

### SW-06：SearXNG結果の重複除去

- 状態：決定
- 決定内容：
  - 正規化URL（`canonical_url`）単位で重複除去する。
  - SearXNG順で最初に出現した結果を採用し、後続の同一`canonical_url`を破棄する。
  - URL正規化処理は`fetch_page`と共通化する。

### SW-07：一部Engine失敗時の扱い

- 状態：決定
- 決定内容：
  - 成功しているEngineの応答結果を返す。
  - 全Engineで検索失敗時は`upstream_error`を返す。

### SW-08：Timeout

- 状態：決定
- 決定内容：
  - SearXNGへの問い合わせTimeout：18秒

### SW-09：検索結果サイズの上限

- 状態：決定
- 決定内容：
  - `snippet`最大長：256文字
  - UTF-8として有効な文字単位で切る。
  - 超過分は切り捨てる。
  - `snippet`自体が存在しない場合は空を許容する。

### SW-10：検索結果の順序

- 状態：決定
- 決定内容：
  - SearXNGが返した順序を維持する。
  - 独自rerankは行わない。

## 関連

- `fetch_page.md`
- `search_local.md`
- `search_mcp_server.md`
- `deployment.md`
