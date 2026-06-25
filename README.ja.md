# furrow

> English: [README.md](README.md)

**furrow** は、repo の中に住むプレーンテキスト・タスクトラッカー。Go 製の単一バイナリで、構造化メタデータを `.furrow/index.json` に、長文の散文を `.furrow/bodies/<id>.md` に分けて持つ。あなたと Claude Code の両方が、git で綺麗に diff できる素のテキストとしてストアを編集できることを最優先に設計している。畝（furrow）を一本ずつ進めるように、レーンを消化していく。

- **module**: `github.com/akira-toriyama/furrow`
- **Go**: 1.23
- **作者 / オーナー**: akira-toriyama (Tommy)

---

## なぜ作るか

GitHub Issues は public で私的メモを置きづらく、他人の issue と混ざり、ローカルのプレーンテキストに対してわずかな剥離がある。一方で「1 ファイルに全部」型の手管理 Task.md は、タスク一覧・設計付録・プロセス規則・経緯が同居して煩雑になり、優先度の手リナンバリングや Open→Done の手アーカイブが苦行になる。

そこで furrow は、**ローカル・プレーンテキスト・自分仕様**のタスク管理を Go で実装する。Issue 連携は持たない。repo に commit して綺麗に git-diff できれば十分、という割り切りである。設計判断の全文は [`ROADMAP.md`](ROADMAP.md)、その根拠は [`MEMO.md`](MEMO.md) にある。

---

## ストレージ（ハイブリッド）

ストアは repo 内の `.furrow/` ディレクトリ一つに収まる。

```
.furrow/
  index.json        # 構造化メタデータ（機械が書く・決定論シリアライズ）
  bodies/<id>.md    # 1 タスク 1 つの長文 markdown 本文（人/エージェントが編集可）
  config.toml       # 人が編集する設定（furrow からは READ のみ）
  seq               # 凍結 id のカウンタ
  archive/          # 退避した古い done タスク（index.json + bodies/）
```

役割分担はこうなっている。

- **`index.json`** = 構造化メタデータだけ。小さく、`jq` や Go で即クエリでき、フィールド単位で diff できる。**唯一の決定論マーシャラ（`core.Marshal`）からしか書かれない。**
- **`bodies/<id>.md`** = 素の markdown。エスケープなし、タスク単位で diff できる。**手でも Claude でも自由に編集してよい。**
- **`config.toml`** = 人が編集する設定。furrow は書き換えず、READ するだけ。
- **`seq`** = 凍結 id の単調増加カウンタ。
- **`archive/`** = 古くなった done タスクの退避先（独自の `index.json` + `bodies/`）。

長文を 1 行のエスケープ文字列に潰す純 JSON 単一ファイルや JSONL を避け、メタと本文を分離するのがこの設計の肝である（根拠は [`MEMO.md`](MEMO.md) §3）。

### 決定論（生命線）

`index.json` の書き込みは `core.Marshal` という**唯一の経路**を通る。契約は以下のとおり。

- key 順 = struct のフィールド宣言順
- 2-space インデント
- `SetEscapeHTML(false)`（CJK や `< > &` がそのまま残る）
- 空のコレクションは `null` でなく `[]`
- ソートは lane → priority → id
- タイムスタンプは UTC・秒単位（RFC3339 の `...Z`、ナノ秒なし）
- 末尾改行あり

この結果、**`furrow` が書いたバイト列と、人や Claude が手編集したバイト列が一致する**。触っていない index を再保存しても git の churn はゼロになる。

---

## インストール

```sh
# Homebrew
brew install akira-toriyama/tap/furrow

# go install
go install github.com/akira-toriyama/furrow/cmd/furrow@latest

# nix
nix run github:akira-toriyama/furrow
```

配布は GoReleaser から Homebrew tap（`akira-toriyama/homebrew-tap`）と nix flake へ流す（ROADMAP Phase 7）。

---

## クイックスタート

```sh
# repo 内でストアを初期化
furrow init

# タスクを追加（id は自動採番・凍結。bodies/<id>.md も作られる）
furrow add "config.toml ローダを書く" --label core --priority 100

# 一覧（lane->priority->id の正準順）
furrow ls

# いま着手できるタスク（terminal レーン以外で、依存が全部 done）
furrow next

# 本文を編集（TTY なら $EDITOR、非対話ならパスを出力する）
furrow edit t-0001

# 詳細を本文付きで表示
furrow show t-0001

# レーン操作・並べ替え
furrow move t-0001 in-progress
furrow reorder t-0001 90
furrow done t-0001
```

---

## コマンド

今日時点で**実装済み**のコマンド（全て動作する）。

| コマンド | 説明 |
|---|---|
| `init` | カレントディレクトリに `.furrow` ストアを作る（`config.toml` + 空の `index.json` + `bodies/`） |
| `add <title>...` | タスクを追加（`--stdin` で標準入力から1行1タスクを一括作成）。id を自動採番し `bodies/<id>.md` を作る |
| `ls`（別名 `list`） | タスクを正準順で一覧 |
| `show <id>` | タスクを markdown 本文付きで表示 |
| `next` | 着手可能なタスク（非 terminal・依存が全部 done）を表示。`--json`/`--ndjson` は各タスクに `reason`（`in_next_lane`・`deps_satisfied`）を付与 |
| `edit <id>` | `bodies/<id>.md` を `$EDITOR` で開く（非対話ならパスを出力） |
| `done <id>` | done レーンへ移動し `closed` を打刻 |
| `move <id> <lane>` | 任意のレーンへ移動 |
| `reorder <id> <priority>` | priority（疎な整数）を設定 |
| `check <id> [index]` | チェックリスト項目をトグル（`--add` で追加・`--off` で外す） |
| `dep <id> <dep-id>` | 依存を追加（id が dep-id を待つ）。`--rm` で削除。循環防止・冪等 |
| `archive` | 古い done タスクを `.furrow/archive/` へ退避（`--yes` なしはプレビュー） |
| `lint` | index↔body の整合・レーン・依存・config を検査 |
| `schema` | `.furrow/index.json` の JSON Schema を出力 |
| `version` | furrow のバージョンを出力 |
| `ui` | 対話 TUI を起動（一覧＋詳細ペイン：移動・フィルタ・done・レーン移動・並べ替え（`K`/`J`）・チェックリストトグル・本文編集） |
| `migrate <file>` | 既存の `Task.md` などを取り込む（dry-run 既定／`--write` で作成・未対応の見出しや `[[wikilink]]` は破棄せず報告） |

### 主なフラグ

- `--status, -s <lane>` — レーンで絞り込み（`ls`）
- `--label, -l <label>` — ラベルで絞り込み（`ls`）。`add` では `-l` 繰り返しでラベルを付与
- `--limit, -n <N>` — 行数の上限（`ls` / `next`。`0` は全件、`next` は `-n1` で先頭だけ）
- `--priority, -p <N>` — `add` で priority を明示（省略時はレーン末尾に追記）
- `--parent <id>` / `--dep <id>`（繰り返し）/ `--ref <file:line|URL>`（繰り返し）/ `--body <md>` — `add` のメタ指定
- `--add <text>` / `--off` — `check` のチェックリスト操作
- `--older-than <days>` / `--yes` — `archive`（`--yes` なしは dry-run プレビュー）

`add` の `--status` 省略時は `config.toml` の `[lanes].default`、`archive` の `--older-than` 省略時は `[archive].older_than_days` が使われる。

---

## CLI 契約（Claude Code / スクリプト向け）

furrow は **非対話がデフォルト**。プロンプトは出さない（TTY 検出は `golang.org/x/term`）。対話 UI は `furrow ui` だけ。

- **`--json`** — read コマンドが JSON を **stdout のみ**に出す。ログ・エラーは stderr へ。
- **`--ndjson`** — タスクを 1 行 1 JSON で出す（list 系）。
- **フィルタ** — `--status/-s`・`--label/-l`・`--limit/-n`。
- **破壊操作ガード** — `archive` は `--yes` がない限りプレビュー（dry-run）に留まる。
- **exit code 契約**:

  | code | 意味 |
  |---|---|
  | `0` | 成功 |
  | `1` | not-found / 結果が空 |
  | `2` | bad-usage / バリデーション失敗（引数を直す。リトライしない） |
  | `3+` | 内部 / IO 障害 |

- **エラー出力** — 非ゼロ時は stderr に次の形を出す（stdout を `jq` に流していても汚染しない）。

  ```json
  {"error":{"code":2,"id":"t-0042","message":"unknown lane \"foo\" (configured: inbox, backlog, ready, in-progress, waiting, done, icebox)"}}
  ```

JSON 出力例:

```sh
furrow ls --json | jq '.[] | select(.status=="ready") | .id'
furrow next --ndjson
furrow show t-0001 --json   # task + body_text
```

ストアの場所は、カレントから親方向に `.furrow/` を探して解決する。`FURROW_DIR` で明示も可能。`edit` のエディタは `FURROW_EDITOR` → `VISUAL` → `EDITOR` → `vi` の順で選ぶ。

---

## 設計の不変条件

- **id は凍結**。`t-0042` 形式（prefix + ゼロ詰めカウンタ、`.furrow/seq` 由来）。**再利用も再採番もしない**。`bodies/<id>.md` のファイル名語幹と 1:1 に対応する。
- **priority は疎な整数**（10 刻みが既定）。並べ替えは `reorder` で 1 フィールドを書き換えるだけ。手リナンバリングは消える。
- **status は `config.toml` のレーン**。Open→Done は値の変更（1 文字 diff）。
- **`done` への移動は `closed` を打刻**。done から外へ移動すると `closed` をクリアする。icebox（温存）のような他の terminal レーンは `closed` を打刻しない（parked と closed は別物）。
- **`next` の定義** = terminal でないレーン、かつ依存（`deps`）が全て done レーンにあるタスク。
- **index ↔ body は 1:1**。`furrow lint` が、本文ファイルのないタスクと、タスクのない孤立本文の双方を報告する。

### スキーマ（`.furrow/index.json`）

```jsonc
{
  "schema_version": 1,
  "tasks": [
    {
      "id": "t-0042",                 // 凍結・bodies/<id>.md の語幹
      "title": "…",                   // 一行サマリ
      "status": "in-progress",        // config.toml のレーン
      "priority": 100,                // 疎な整数・並べ替えはこれだけ
      "labels": ["core", "cli"],
      "parent": "t-0001",             // 任意（omitempty）
      "deps": ["t-0003"],             // 依存（next が ready 判定に使う）
      "refs": ["docs/x.md#L10", "https://…"], // file:line / URL
      "checklist": [ { "text": "…", "done": false } ],
      "created": "2026-06-25T00:00:00Z",
      "updated": "2026-06-25T00:00:00Z",
      "closed": null,                 // open の間は null・done で打刻
      "body": "bodies/t-0042.md"      // 相対パス（本文そのものではない）
    }
  ]
}
```

正準スキーマは `furrow schema` が出力する（draft 2020-12）。これが正本で、`docs/schema/furrow.index.v1.json` は commit 済みのコピー。CI が両者を diff して drift を防ぐ。

---

## アーキテクチャ（hexagonal）

ports & adapters。依存は内向きにのみ流れる。詳細図は `docs/architecture.md`（家風どおり整備予定）。

```
cmd/furrow/main.go                 = os.Exit(cli.Execute()) のみ
  └─ internal/cli   (cobra アダプタ)        ┐
     internal/tui   (bubbletea v1・対話 UI)         ┘ presentation
        └─ internal/app   (唯一の mutation funnel・CLI/TUI 共通)
              ├─ internal/config        (config.toml ロード・clamp-don't-reject)
              ├─ internal/store/fsstore (FS に触る唯一の package)
              └─ internal/store/memstore (in-memory fake)
                    └─ internal/core  (純ドメイン・stdlib のみ)
```

- **`internal/core`** — 純ドメイン。`Index` / `Task` 構造体、唯一の `core.Marshal` 経路、`Store` / `Clock` などの port（interface）、validate、index 操作を持つ。**標準ライブラリしか import しない**（cobra・bubbletea・os・filepath は禁止）。
- **`internal/config`** — `config.toml` を読むだけ。clamp-don't-reject。
- **`internal/store/fsstore`** — **FS に触る唯一の package**。atomic write（同一ディレクトリの tmp + rename）、本文の lazy load、`.furrow/seq` による `NextID`。
- **`internal/store/memstore`** — in-memory の fake（テスト・dry-run 用）。
- **`internal/app`** — **唯一の mutation funnel**。CLI も TUI も必ずここを経由する。frozen id・正準順・closed 打刻・body↔index の対応をここで一括管理する。
- **`internal/cli`** — cobra アダプタ。
- **`internal/tui`** — bubbletea v1 の対話 UI（`furrow ui`）。CLI と同じく presentation 層で、mutation は必ず `internal/app` 経由。
- **`internal/schema`** — JSON Schema のソース。`internal/version` — ビルドバージョン（リンカ注入。from-source は `dev`）。

---

## 設定（`.furrow/config.toml`）

`furrow init` がテンプレートを書き出す。furrow は **READ するだけ**で、書き換えない。方針は **clamp-don't-reject**：不明なキーは無視し、範囲外の値は安全な既定へ丸めたうえで `furrow lint` が警告する（タイプミスでツールが壊れない）。

```toml
[lanes]
order = ["inbox", "backlog", "ready", "in-progress", "done", "icebox"]
default = "inbox"           # `furrow add` が割り当てるレーン
done = "done"               # `furrow done` の移動先（closed を打刻）
terminal = ["done", "icebox"]  # `next` で着手不可とするレーン

[priority]
step = 10
default = 100

[ids]
prefix = "t-"
width = 4                   # t-0042

[archive]
older_than_days = 30

[ui]
theme = "auto"              # auto | dark | light
```

`[lanes].order` は status の enum 兼ソート順位を兼ねる。`NO_COLOR` は `[ui].theme` の値に関係なく常に尊重される。

---

## Claude Code 連携

furrow の連携層は意図的に薄い。**MCP も plugin も作らない**（solo には過剰）。`CLAUDE.md` の短いブロックと、全 read コマンドの `--json` だけが連携面である（根拠は [`MEMO.md`](MEMO.md) §4）。

Claude（やエージェント）に守らせるルール:

- **`index.json` を手編集しない。** 単一のマーシャラが所有しており、手編集は git の churn を生む。`add` / `move` / `reorder` / `done` / `check` などのコマンドで変更する。
- **`bodies/*.md` は編集してよい。** 長文の散文はここに置く。
- 状態変更は必ずコマンド経由。出力を機械処理するなら `--json` / `--ndjson` を使う。

---

## 開発

```sh
go build ./...
go test ./...
go run ./cmd/furrow --help
```

決定論は load-bearing な不変条件なので、golden-file の往復テスト（write → read → write がバイト一致）と「マーシャラ経路は 1 本だけ」を確認する CI ガードで守る。`index.json` を書く経路を `core.Marshal` 以外に増やしてはならない。

### コミット規約

gitmoji + Conventional Commits。形式は次のとおり（gitmoji は `:code:` のテキスト形式で書く）。

```
<:gitmoji:> <type>(<scope>)<!>: <subject>
```

`scripts/hooks/commit-msg`（`git config core.hooksPath scripts/hooks` で有効化）と CI が検証する。

### ハウススタイル

- バイリンガル README: `README.md`（English）+ `README.ja.md`（日本語）。両方を ship する。
- `docs/` に architecture / glossary / non-goals を置く。
- 外部参照には `(reviewed YYYY-MM-DD)` の鮮度タグを付ける。

> 上記の `docs/`・`CLAUDE.md`・`scripts/hooks`・CI ワークフローは家風として整備していく方針で、一部はまだ追加されていない。実装の現況とフェーズ計画は [`ROADMAP.md`](ROADMAP.md) を正本とする。

---

## ステータス

core・config・store・app・CLI・TUI（`furrow ui`）・`migrate` が動作する。残りは Phase 7（パッケージング）と Phase 8（Web）。詳細なフェーズ進捗は [`ROADMAP.md`](ROADMAP.md) を参照。

## ライセンス

MIT（予定）。作者 akira-toriyama (Tommy)。
