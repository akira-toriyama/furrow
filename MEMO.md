# furrow — MEMO（調査ログ）

> 調査・意思決定の根拠を時系列＋トピックで蓄積する作業ログ。[`ROADMAP.md`](ROADMAP.md) が「決定」、ここが「なぜそう決めたか」。新しい調査は随時追記する。

---

## 1. build vs buy（なぜ自作するか）

Claude Code / GitHub フレンドリーなタスク管理を 5 観点（GitHub-native / markdown-git / CC 専用 / MCP web app / 古典 CLI）で調査し、主要候補の連携主張を裏取りした（adversarial verify）。

確定制約（solo・Issue 不使用・非バイナリ・JSON 等プレーンテキスト・CLI+TUI+将来 web・brew/nix）を当てた結果のスコア:

| 候補 | スコア | 要点 |
|---|---|---|
| Backlog.md | 8/10 | md+frontmatter・`backlog board`(TUI)+`backlog browser`(編集可 web)・MCP・成熟(★5.8k)。痛みは溶けるが **JSON でない / 自前 TUI（bubbletea でない）/ Node 製 / [[wikilink]] 非対応**。 |
| **自作 Go** | 7/10 | **全要件を literal に満たす唯一**（JSON store・bespoke bubbletea TUI・native [[wikilink]]・自分仕様 lane）。代償は Backlog.md の ~90% を作り直す保守コスト。 |
| taskmd (driangle) | 5/10 | Go 単一バイナリ＋frontmatter＋web＋Claude plugin で**理想形に最も近い**が、pre-1.0・★48・単独作者・web は read-only。**フォーク/参考の土台**として有用。 |
| dstask | 2/10 | store が repo 外（`~/.dstask`）・UI/agent 連携なし＝失格。 |

**結論**：データ上は「Backlog.md を買え」（90% カバー）。ただしトミーは JSON / bubbletea / 自分仕様 lane / native [[wikilink]] を must-have とし、Claude Code の Go 実装力で自作が安価（~2-4 日 / ~2,000 LOC 見込み）なので **build を選択**。

落とした他カテゴリ：GitHub Issues / gh CLI(store) / GitHub MCP / Linear / Notion / CCPM / Spec Kit（Issue・クラウド前提）、Taskwarrior（v3 が binary SQLite）、git-bug（git object 埋め込みで「ファイル編集」できない）、ultralist（JSON+Go で理想形だが 2020 で死亡）。

---

## 2. 命名（furrow に決めた経緯）

19 候補を生成し、全件「有名 CLI / npm / brew / 企業ブランド」との衝突を web で裏取り。バイナリ名のかぶりを最優先。

- **採用: `furrow`** — 畑の畝。並んだ畝＝status レーン、畝を一本進める＝消化していく、の比喩がモデルに直結。**衝突ほぼ無**（休眠 1-star の Go repo のみ・brew/npm 空き）。canon/facet と同じ土の手触り。
- 次点: `cairn`（道標の石積み・美学最強だが `cairn-dev/cairn` という AI coding agent repo が同領域）/ `docket`（意味ど真ん中・Rust 製ニッチ binary あり）/ `lode`（母鉱脈・トミー自身 atelier issue で次点に挙げていた・"load" と同音）/ `roster`（当番表・binary 空きだがやや事務的）。
- **検証で落とした**: `slate`（slatedocs/slate ★36k + SlateJS）/ `ledger`（`brew install ledger` 会計 CLI と正面衝突）/ `blaze`（brew formula 占有）/ `marker`（marker-pdf ★36k + pindexis/marker が両方 `marker` を占有）。

---

## 3. ストレージ形式（ハイブリッドに決めた理由）

判定基準：①長文 prose の git diff/merge ②機械クエリ容易性 ③人/Claude が壊さず編集 ④凍結 ID。

- ❌ 単一 `tasks.json`：長文が `\n` エスケープの 1 行文字列 → 全行 churn・Claude が 300 行エスケープを編集すると壊す。
- ❌ JSONL：prose 問題は不変（1 物理行）。
- ❌ md+frontmatter（純）：prose は綺麗だが「open を priority 順で」が全ファイル走査、横断更新が多ファイル書換。
- ✅ **ハイブリッド**：`index.json`（メタのみ・小・jq/Go で即クエリ・field 単位 diff）+ `bodies/<id>.md`（素の md・エスケープ無し・タスク単位 diff）。Task.md の「1 ファイルに全部」を構造的に分解する。

**「JSON 以外の推奨は？」** → index は JSON が最適（機械が書く・`jq`・Claude・決定論）。**human 設定は TOML**（`.furrow/config.toml`＝家風の config.toml 駆動に一致）。本文は md。YAML/SQLite は却下。

determinism が生命線：マーシャラ 1 経路・キー順固定・`SetEscapeHTML(false)`・`[]` not null・golden-file テスト・CLAUDE.md で `index.json` 手編集禁止。

---

## 4. Claude Code / CLI 連携の設計規則

- **MCP も plugin も作らない**（2026 ガイダンス：plugin はチーム配布用「solo は使うな」、MCP は cross-agent/remote/auth 用 — どれも該当せず）。**plain CLI + ~15 行 CLAUDE.md ブロック**が連携層。
- `--json` を全 read コマンドに。**JSON は stdout のみ**（log/spinner は stderr）。`--ndjson`/`--field`/`--status`/`--limit` でフィルタを flag 化（jq 体操を減らす）。`schema_version` を持たせる。
- **非対話デフォルト**：`term.IsTerminal` で TTY 検出・**プロンプトしない**。TUI は `furrow ui` のみ。破壊操作は `--yes`（無いと exit 2）。`NO_COLOR` 尊重。
- **exit code 契約**：0 成功 / 1 not-found・empty / 2 bad-usage・validation（リトライせず引数を直す）/ 3+ 内部・IO。非ゼロ時は `{"error":{"code","id","message"}}` を stderr へ。
- **決定論順序**（lane→priority→id・map order/readdir order 禁止）・RFC3339 UTC。

---

## 5. UI の方針

- **TUI = charmbracelet bubbletea v1 + bubbles v1 + lipgloss v1（v1 固定**・v2 の getter/setter 移行税を回避）。`bubbles/list`（レーン+ページング+fuzzy）/ `viewport`（本文・glamour で md レンダ）/ `key`+`help`（キーバインド+ヘルプ）。編集は `$EDITOR` shell-out（`textarea` 埋め込みより低コスト）。`table` は当面スキップ。
- **web は後回し**。やるなら **Go `net/http` + `embed.FS`** で `index.json` を読む静的 SPA（~50 行・node 不要・単一バイナリ）。read-only から。
  - 却下：templ+htmx（codegen 工程増）、Wails（webview+JS toolchain 同梱で重い）。**sleek は React+Electron** なので stack は真似ない（todo.txt モデルだけ参考）。
- 将来の React 系 UI は `index.json` を読むだけで成立する（だから JSON index が効く）。

---

## 6. 参考にするリポジトリ（Phase 1 で clone & 学ぶ）

> 「良いところを採り、不満を改善して実装する（重要）」。各リポの **採る長所 / 直す不満** を Phase 1 workflow でここに追記する。

### 調査済み TODO CLI（全 clone OK）
- **Backlog.md** (MrLesk) — md-per-task+frontmatter / `board` TUI / `browser` web / MCP / AC・DoD チェックリスト。**採**: 1 タスク 1 ファイル・spec 駆動（説明+受け入れ条件+実装計画）・agent 指示ファイル。**直**: JSON でない・自前 TUI・Node 依存・[[wikilink]] 無し。
- **taskmd** (driangle) — Go・frontmatter・web dashboard・Claude plugin/MCP。**採**: Go 単一バイナリ構成・`next`/`do` ループ。**直**: web read-only・★少・schema が JSON でない。
- **dstask** (naggie) — Go 単一バイナリ・git-native・YAML+md。**採**: 単一バイナリ・git sync/undo。**直**: store が repo 外・UI/agent 連携なし。
- **todo.txt-cli** / **topydo** — todo.txt フォーマット。**採**: 1 行 1 タスクの究極のシンプルさ・grep 容易・stable ecosystem。**直**: 長文 prose を持てない・依存/繰り返しが非標準。
- **ultralist** — Go・`.todos.json`。**採**: JSON store の発想・GTD モデル。**直**: 2020 で死亡・JSON 単一ファイルの diff。
- **git-bug** — Go・git object 埋め込み。**採**: 分散・offline・TUI。**直**: ファイル編集できない（agent 不適）。

### Go 有名リポ / ベストプラクティス（https://github.com/topics/go）
- **charmbracelet/bubbletea, bubbles, lipgloss, glamour, gum, soft-serve** — TUI の作法・見た目の基準。
- **spf13/cobra**（+ `carapace`/補完）— サブコマンド・flag・help・補完。
- **junegunn/fzf, jesseduffield/lazygit, cli/cli(gh)** — 端末 UX・キーバインド・JSON 出力・配布。
- **goreleaser/goreleaser** — brew tap / nix / クロスビルドのリリース。
- Go 一般：標準レイアウト（`cmd/`, `internal/`）・table-driven tests・golden files・`errors.Is/As`・`context`・`encoding/json` の決定論シリアライズ・`golang.org/x/term`。

### 自分のリポ（家風の参考）
- **chord / perch / wand / facet** — hexagonal（ports & adapters）・**config.toml 駆動**・gitmoji+Conventional commits・`run.sh`・`./` 実機検証文化。
- **atelier / sill** — 共有 lib・reusable CI・`CLAUDE.common.md` sync・cliff.toml・commit-msg hook。furrow も将来 sill の作法に寄せられる（third-party 依存ゼロ志向・自分の共有 lib は OK）。
- **swift-toml-edit** — lossless TOML round-trip。`config.toml` を将来 furrow から編集するなら同思想（今は read のみで十分）。

---

## 7. 工数見積もり（Claude Code 実装・トミー solo review）

MVP（core + CLI + migrate + TUI + CLAUDE.md）で **~2-4 日 / ~1,800-2,800 LOC Go**。山は migrate パーサと TUI 仕上げ。web/React は別途（JSON core があるので後付け半日）。ただし**品質重視・時間かけてOK**方針なので、各フェーズで test green を保ちつつ丁寧に進める。

---

## 8. 環境メモ

- workspace: `/Volumes/workspace/github.com/akira-toriyama/`（facet 等と並ぶ）。furrow clone 済み。
- toolchain: **Go 1.23.3 darwin/arm64**・**Homebrew 6.0.3** あり・**nix 未導入**（nix flake は用意するがローカル検証不可 → CI/別環境）。
- Projects #5 = "roadmap"・**private**・106 items。Status = `📥 Inbox / 📋 Backlog / ✅ Ready / 🔨 In Progress / ✔️ Done / 🧊 Icebox`。本文は長文 markdown（例 issue #227）→ ハイブリッド方針の妥当性を裏付け。

---

## 9. 追記ログ

- 2026-06-25: 初版。build-vs-buy / 命名 / ストレージ / 連携 / UI / 参考リポ / 工数を記録。Phase 1（参考リポの clone & 学び）の詳細を次に追記予定。
