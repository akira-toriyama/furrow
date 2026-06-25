# furrow — ROADMAP

> **furrow** = repo の中に住むプレーンテキスト・タスクトラッカー。**JSON index + 1 タスク 1 markdown 本文**を、CLI と bubbletea TUI から操作する。あなた（トミー）と Claude Code の両方が編集できることを最優先に設計する。畝（furrow）を一本ずつ進めるように、レーンを消化していく。
>
> **正本（single source of truth）。** セッション跨ぎの引き継ぎはこの `ROADMAP.md` と [`MEMO.md`](MEMO.md) に集約する（**未達成を暗黙にしない**）。`ROADMAP.md` = 要件・決定・フェーズ計画の正典。`MEMO.md` = 調査ログ・参考リポからの学び・根拠の蓄積。

---

## 🎯 なぜ作るか（背景）

現状は **GitHub Projects #5（roadmap・106 items）+ Issues** と、facet の手管理 `Task.md`（300 行超）でタスクを追っている。痛み:

- Issue は **public**（私的メモを置きづらい）・**他人の issue と混ざる**・ローカルのプレーンテキストに対して **ラグ／わずかな剥離**がある。
- `Task.md` は 1 ファイルに *タスク一覧 + 設計付録 + プロセス規則 + 経緯* が同居 → **煩雑でしんどい**。優先度の手リナンバリング、Open→Done の手アーカイブが苦行。

→ **ローカル・プレーンテキスト・自分仕様**のツールを Go で自作する。買う側（Backlog.md 等）の調査結論と却下理由は [`MEMO.md`](MEMO.md) に記録済み。

---

## 🧱 ハード要件（トミー指定）

- **Claude Code フレンドリー** / **CLI フレンドリー** / **TUI 欲しい**。
- **GitHub フレンドリー = 非バイナリならOK**（repo に commit して綺麗に git-diff できればよい）。**Issue 連携は不要**。
- **保存はプレーンテキスト**：構造化メタ = **JSON**、長文詳細 = **Markdown**。
- 扱うデータ量は **Projects #5 程度**で十分（JSON で足りる）。
- **完了済みは残すのが理想**だが、しばらくしたら **Archive もOK**（それで軽さをキープ）。
- 優先度：**CLI・TUI が高**、**web UI / (React + Electron) は低**（将来は React 系 UI が欲しい）。
- Go の TUI は **おすすめのでOK・見た目が良いこと**。
- **brew / nix でインストール**できること。
- 調査した **CLI 系 TODO アプリを全部 clone してよい**。**良いところを採り、不満を改善して実装する（重要）**。
- **Go の有名リポ**を確認し良い所を参考に（何個か clone OK）。**Go ベストプラクティス**を確認する。
- **私のリポジトリも何個か参考に**する（hexagonal・config.toml 駆動・atelier/sill の家風）。

### 進め方

- **1 セッションで完了しなくてよい** / **破壊的変更OK** / **品質重視** / **時間かけてOK** / **未達成を暗黙にしない**。

---

## 🔑 確定した設計判断（詳細根拠は MEMO.md）

1. **ストレージ = ハイブリッド**。`.furrow/index.json`（構造化メタのみ）+ `.furrow/bodies/<id>.md`（長文 prose）。
   - 純 JSON 単一ファイルや JSONL は **長文 prose を 1 行エスケープ文字列に潰す** → git diff が全行 churn ＝ Task.md の痛みを JSON で再現、かつ Claude が編集で壊す。だから本文は md に分離する。
2. **「JSON 以外の推奨は？」への回答**：
   - **index（機械が書く・手編集しない）= JSON** が最適（`jq`・Claude・決定論シリアライズ）。
   - **human が触る設定 = TOML**（`.furrow/config.toml`）＝ あなたの **config.toml 駆動の家風**に合わせる（perch/chord/wand/canon と同様）。
   - **本文 = Markdown**。
   - YAML は却下（空白依存で Claude 編集が壊れやすい）。SQLite は却下（バイナリ）。
3. **ID は凍結**（`t-0042`・ファイル名の語幹）。**再利用・リナンバリングしない**。
4. **priority は独立した疎な整数フィールド**（10 刻み: 100,110,120）。並べ替え = 1 フィールド編集 → **手リナンバリング消滅**。
5. **status はフィールド**。Open→Done は値の変更（1 文字 diff）→ **手アーカイブ消滅**。レーン定義は `config.toml` 駆動。
6. **決定論シリアライズ**：キー順固定・2-space・`SetEscapeHTML(false)`（CJK/記号そのまま）・`[]` not null・lane→priority→id でソート・末尾改行。アプリ書き込みと手/Claude 編集が **byte 一致** → 無駄な git churn ゼロ。
7. **Claude Code 連携 = MCP も plugin も作らない**（solo には過剰）。**~15 行の CLAUDE.md ブロック**＋ `--json` を全 read コマンドに、が連携層。
8. **非対話デフォルト**（TTY 検出・`furrow ui` のみ TUI 起動・破壊操作は `--yes`）。**exit code 契約**（0/1/2/3+）。
9. **アーカイブ**：done はホット index に残す。`furrow archive --older-than 30d` で `.furrow/archive/` へ退避し軽さを保つ。

---

## 🗂 スキーマ（Projects #5 を土台に）

Projects #5 の実フィールド（`Status: 📥 Inbox / 📋 Backlog / ✅ Ready / 🔨 In Progress / ✔️ Done / 🧊 Icebox`, Labels, Parent issue, Sub-issues progress, Created/Updated/Closed）を写し取る。

```jsonc
// .furrow/index.json
{
  "schema_version": 1,
  "tasks": [
    {
      "id": "t-0042",                 // 凍結・bodies/<id>.md の語幹
      "title": "…",                   // 一行サマリ
      "status": "in-progress",        // config.toml のレーン enum（既定: inbox/backlog/ready/in-progress/done/icebox）
      "priority": 100,                 // 疎な整数・並べ替えはこれだけ
      "labels": ["canon", "zmk"],     // = Projects "Labels"
      "parent": "t-0001",             // = Parent issue（任意）
      "deps": ["t-0003"],             // 依存（task next が ready 判定に使う）
      "refs": ["docs/x.md#L10", "https://…"], // file:line / URL
      "checklist": [ { "text": "…", "done": false } ], // = Sub-issues progress 相当
      "created": "2026-06-25T00:00:00Z",
      "updated": "2026-06-25T00:00:00Z",
      "closed": null,
      "body": "bodies/t-0042.md"
    }
  ]
}
```

不変条件：`index` の id ⇔ `bodies/<id>.md` が 1:1（`furrow lint` で検証）。

---

## 🛠 フェーズ計画（未達成を暗黙にしない）

> 各フェーズは独立に build/test green を保つ。1 セッションで終えなくてよい。進捗はこのチェックボックスで管理。

### Phase 0 — Setup ✅
- [x] 空 repo 作成（public・`akira-toriyama/furrow`）
- [x] `ROADMAP.md` / `MEMO.md` 初版
- [x] `go.mod`（module path `github.com/akira-toriyama/furrow`・Go 1.23・`GOTOOLCHAIN=local`）・`.gitignore`・`LICENSE`(MIT)
- [x] CI（`build.yml`/`commit-lint.yml`/`release.yml`）・commit 規約（`scripts/hooks/commit-msg`・`docs/commit-convention.md`）・`cliff.toml`・`.golangci.yml`(v2)・`.editorconfig`・`dependabot.yml`・PR テンプレ を家風に寄せた
- [x] 参考リポの clone & 学び（Phase 1 workflow）を `MEMO.md §8-11` に統合

### Phase 1 — Study（重要） ✅
- [x] study-engine（非 TUI UI / スキーマ駆動）を実読 → `MEMO.md §8`
- [x] 家風 repo（chord/facet/atelier/jig/perch）を並列解析し hexagonal・config.toml 駆動・cliff/commit-msg/CI/packaging の移植方針を抽出 → `MEMO.md §10`
- [x] facet `Task.md` を実読し migrate 仕様を確定 → `MEMO.md §11`
- [~] 調査済み TODO CLI（Backlog.md/taskmd/dstask/…）の「採る長所/直す不満」は MEMO §1/§6 の調査時サマリで代替（個別 clone は未実施・Phase 5 migrate 設計時に taskmd を参照予定）

### Phase 2 — Design lock ✅
- [x] スキーマ確定（`internal/schema.IndexV1` = 正本・`docs/schema/furrow.index.v1.json` に committed・`furrow schema` で emit・CI drift guard）・`config.toml` 仕様（clamp-don't-reject）・determinism 規則（single marshaller）
- [x] アーキテクチャ確定（hexagonal・`internal/core` 純粋・`internal/app` が唯一の mutation funnel・CLI/TUI が共有）→ `docs/architecture.md`
- [x] CLI コマンド面確定（`--json` 全 read 対応・exit code 契約 0/1/2/3+）

### Phase 3 — Core lib（`internal/core` + `store` + `config` + `app`）✅
- [x] Index/Task struct・決定論マーシャラ（`core.Marshal` 単一経路）・atomic write（tmp+rename・`fsstore`）・id 採番（`.furrow/seq`）・body lazy load
- [x] golden-file テスト（決定論・往復 byte 一致）・table-driven テスト・`memstore` fake・`scripts/check-marshal-singlepath.sh` guard

### Phase 4 — CLI ✅（`migrate` を除く）
- [x] `add / ls / show / next / edit / done / move / reorder / check / archive / lint / init / schema / version / ui(stub)`
- [x] `--json`/`--ndjson`/`--status`/`--label`/`--limit`・非対話デフォルト・`edit` は非 TTY で path 出力・`archive` は `--yes` ガード・STDERR エラーオブジェクト
- [ ] `migrate` は Phase 5 へ。`--field` は未実装（jq で代替可・必要なら後日）

### Phase 5 — migrate（取り込み） ⏳ **未着手**
- [ ] `furrow migrate ./Task.md --dry-run`（レーン→status・`## 付録`→body・プロセス→CLAUDE.md・経緯→docs/・[[wikilink]]→凍結 id）。仕様は `MEMO.md §11` に確定済
- [ ] （任意）`furrow import --from-gh-project 5`（Projects #5 を初期投入）

### Phase 6 — TUI（bubbletea） ⏳ **stub のみ**
- [ ] `furrow ui`：現状は「未実装」を明示して exit 2 する stub（`internal/cli/ui.go`）。`internal/tui` package は未作成
- [ ] list（レーン+fuzzy）+ detail viewport（glamour で本文）+ status/priority/reorder キー + `$EDITOR` shell-out + checklist toggle・bubbletea **v1 固定**

### Phase 7 — Packaging 🟡 **設定済・未検証**
- [x] GoReleaser（`.goreleaser.yaml`・`brews:` → `akira-toriyama/homebrew-tap`）・`release.yml`・`packaging/homebrew/furrow.rb`（参考コピー）
- [x] Nix flake（`flake.nix`・`nix run`/`nix profile install`）— ※`vendorHash` は placeholder（nix 未導入＝ローカル検証不可・CI/別環境で確定要）
- [ ] 実際の release 実行（タグ push）と tap/nix の動作確認は未

### Phase 8 — Web / React UI（優先度低・将来） 🧊
- [ ] まず Go `net/http` + `embed.FS` で `index.json` を読む静的ビューア（read-only）
- [ ] 将来 React 系 UI（study-engine の renderer 構成を写経・host は Electron vs Go 静的を再検討＝MEMO §8）

### CLAUDE.md 連携ブロック（Phase 4 と同時）✅
- [x] store は CLI 管理・`index.json` 手編集禁止・`bodies/*.md` は編集可・正準コマンド列・exit code・`--json` 必須、を `CLAUDE.md` 冒頭ブロックに明記

---

## ❓ Open questions

- コマンドのタイプ数（`furrow` は 6 文字）：短いエイリアス（例 `fw`）を用意する？ → `config.toml` か brew formula で。**現状**：`ls` に `list` alias のみ。`fw` は未提供（要望あれば brew formula で symlink）。
- 既定 status レーンは Projects #5 の 6 段（inbox/backlog/ready/in-progress/done/icebox）でよい？ → **採用済**（`config.toml [lanes].order`・切替可能）。
- `furrow import --from-gh-project 5` を初期移行で使うか（106 items の取り込み）。→ Phase 5 で判断。
- **`next` の意味（要トミー判断）**：現状 `next` = 「terminal でない（done/icebox 以外）レーン ＋ deps が全部 done」を canonical 順（lane→priority）で列挙。**inbox/backlog も含む**ため、canonical 順だと intake が先に並ぶ。「next＝着手可能」をより厳密にするなら ① ready/in-progress のみ対象 ② `next` 専用に in-progress→ready→… の逆順表示、等が考えられる。今は単純・明確な定義を採用し保留。
- **`--field`**（MEMO §4 で言及）は未実装。jq で代替可能なため後回し（要望あれば read コマンドに追加）。
- **time 表示**：index は RFC3339 UTC（決定論）。`show` の人間向け表示はローカル整形でなく UTC `2006-01-02 15:04`。TZ 表示が要るか要確認。
