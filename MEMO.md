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

## 8. study-engine（emmett-lathrop-brown）— 非 TUI UI / スキーマ駆動の参考

トミー提供（2026-06-25）。**TS + Electron + React + Vite** の Claude Code 駆動アプリ（SM-2 間隔反復）。furrow に直結する学び:

- **mechanism(public) / data(private) 分離**：`study-engine`（仕組み）と `study-log`（個人データ）を repo で分ける。furrow は「データは利用者の repo の `.furrow/`」なので構造は違うが、「素の JSON+md・ビューアロックインなし」の哲学は同じ。
- **JSON(構造化) + md(人の散文) の使い分け**：「問題は構造化が要る→1問1JSON、`learned/` は散文→md」。furrow の `index.json` + `bodies/<id>.md` と同一思想で裏取り。
- **履歴(事実・追記 JSONL) vs 状態(計算結果・再構築可能キャッシュ)**：`reviews.jsonl`（SoT）→ `state.json`（キャッシュ）を `rebuild-state`（既定 dry-run・smoke で往復検証）で再生。furrow には決定論 golden 往復テスト（write→read→write が byte 一致）として写経。
- **JSON Schema 同梱（study-engine は draft-07・`additionalProperties:false`・pattern/required）**：`schema/*.schema.json`。furrow も `docs/schema/furrow.index.v1.json` を同梱し `furrow schema` で emit + CI drift guard。**furrow は draft 2020-12 を採用**（`internal/schema/schema.go` が正本）。
- **store がパス構築を集約（`domainDir()`）**：呼び出し側は `subjects/` を直書きしない。furrow も `fsstore` が `.furrow/bodies/<id>.md` を一手に組む（呼び出し側ハードコード禁止）。
- **`types.ts` は runtime import ゼロ → renderer が `import type` で共有**：Go では `internal/core` を純粋に保ち CLI/TUI 両アダプタが依存、と等価。
- **不正ファイルは graceful skip + 決定論 sort**：furrow lint は crash でなく報告。
- **非 TUI UI（トミーの主眼）**：React renderer（`App/Session/Summary/Heatmap/Markdown(react-markdown+remark-gfm)/ChatPanel`）+ 薄い `api.ts` 境界（`window.api`）。furrow の Phase 8 web/React UI は **この component 構成（board/list→detail→md レンダ→stats）を写経**し `index.json` を読む。
  - ⚠️ **host の決定は Phase 8 で再検討**：study-engine は **Electron**。MEMO §5 は furrow 用に Electron を却下し **Go `net/http`+`embed.FS` 静的 SPA**（単一バイナリ維持）を推した。トミーが study-engine を「非 TUI UI の参考」として再提示したので、**React の中身は写経・host(Electron vs Go 静的)は Phase 8 の論点**として明示保留。`react-markdown`+`remark-gfm` での本文レンダは host 非依存で採用。

---

## 9. 環境メモ

- workspace: `/Volumes/workspace/github.com/akira-toriyama/`（facet 等と並ぶ）。furrow clone 済み。
- toolchain: **Go 1.23.3 darwin/arm64**・**Homebrew 6.0.3** あり・**nix 未導入**（nix flake は用意するがローカル検証不可 → CI/別環境）。**goreleaser / git-cliff も未導入**（Phase 7 で brew 導入）。`GOTOOLCHAIN=local` で固定（go.mod は `go 1.23`・deps は cobra v1.8.1 / x/term v0.27.0 等 1.23 互換に pin）。
- git remote は SSH host alias `github.com.akira-toriyama`（複数アカウント設定）。gh は `akira-toriyama` で認証済。
- **`akira-toriyama/homebrew-tap` 既存**（`Formula/` に CLI＝jig 等・`Casks/` にアプリ）。方式は各 repo の `packaging/homebrew/<name>.rb` + `.github/workflows/update-tap.yml` で release 時に url/sha256 自動更新。**furrow は Go ＝ GoReleaser の `brews:` ブロックで tap に formula を push する方が綺麗**（手 sed-bump 不要）。
- Projects #5 = "roadmap"・**private**・106 items。Status = `📥 Inbox / 📋 Backlog / ✅ Ready / 🔨 In Progress / ✔️ Done / 🧊 Icebox`。本文は長文 markdown（例 issue #227）→ ハイブリッド方針の妥当性を裏付け。
- facet `Task.md`（169 行）を実読＝**migrate の仕様確定**（§10）。

---

## 10. Phase 1 調査の結論（家風リポ → Go 移植・2026-06-25 workflow）

chord / facet / atelier / jig / perch を並列解析。トミーの repo は **Swift だが meta-pattern は Go に綺麗に転用可**。確定した移植方針:

### アーキテクチャ（hexagonal の背骨）
chord の `ChordCore`(純) / `ChordAdapterMacOS`(OSの唯一の置き場) / `ChordAdapterTest`(非 testTarget の fake) / `ChordApp`(@main) を Go に写像:
- **`internal/core`** = 純ドメイン（`encoding/json`・`sort`・`time`・`errors` のみ。cobra/bubbletea/os/filepath 禁止）。** port は core 内の interface**（`Store`/`Clock`/`IDGen`）。「レイヤを跨ぐ＝interface が足りない合図」。
- **`internal/store/fsstore`** = FS に触る唯一の package（atomic tmp+rename・lazy body・ランダム id 生成 / 共有カウンタなし）。`internal/store/memstore` = test/非test 兼用の in-memory fake（chord の AdapterTest を非 testTarget にした作法）。
- **`internal/config`** = `.furrow/config.toml` を読むだけ（clamp-don't-reject・`Effective*` accessor）。
- **`internal/app`** = coordinator（Store+Config 保持・**唯一の mutation funnel**）。CLI/TUI は必ずここ経由。
- **`internal/cli`**(cobra) / **`internal/tui`**(bubbletea v1) = presentation。TUI はファイルを直接書かない。
- `cmd/furrow/main.go` = `os.Exit(cli.Execute())` のみ。
- `docs/architecture.md` に ASCII レイヤ図 + 「What's NOT in scope」（chord/facet 作法）。

### 依存（proxy で解決確認・bubbletea は v1 系で固定）
`cobra` / `bubbletea v1.3.10`+`bubbles v1.0.0`+`lipgloss`+`glamour`（bubbles v1.0.0 の go.mod 自体が bubbletea v1.3.10 要求＝v2/charm.land 経路を踏まず綺麗に揃う）/ `pelletier/go-toml/v2`（TOML は 1 ライブラリのみ・unknown key 無視＝clamp 可能）/ `golang.org/x/term`（TTY 検出）。**index は stdlib `encoding/json` のみ**（第三者 JSON 禁止＝決定論経路を 1 本に）。

### 決定論（load-bearing invariant）
`core.Marshal(*Index)` を **唯一のシリアライズ経路**に：struct field 順＝JSON key 順 / 2-space / `SetEscapeHTML(false)`（CJK そのまま）/ `[] not null`（Canonicalize で nil slice を `[]` に）/ sort `lane→priority→id` / 末尾改行。`json.Marshal(Index)` を他所で呼ばない。**golden 往復テスト** + `scripts/check-marshal-singlepath.sh`（grep guard）+ schema drift test（`furrow schema` 出力 == `docs/schema/...`）で守る。time は **UTC・ナノ秒0**（RFC3339 で `...Z` 安定）。

### 家風ガバナンス（ほぼ verbatim 移植）
- **verbatim**: `cliff.toml`（gitmoji strip preprocessor + 非 bump type skip・rolling-draft・CHANGELOG 非コミット）/ `scripts/hooks/commit-msg`（`<:gitmoji:> <type>(<scope>)<!>: <subject>` の POSIX-sh 検証）/ `docs/commit-convention.md` / `docs/plans/README.md`（1 タスク 1 ファイルの multi-session 運用）/ `.github/dependabot.yml`（gomod+actions・`:arrow_up: chore` prefix）/ `.editorconfig`。
- **adapt to Go**: `run.sh`/`build.sh`/`install.sh`（daemon/pkill/codesign は **skip**＝furrow は CLI）/ `.github/workflows/{build,commit-lint,release}.yml`（GoReleaser 化）/ `CLAUDE.md`（家風スケルトン: What this is → Build/run → 参照(glossary/non-goals) → **Non-obvious constraints — read before editing**（layer rules・single marshaller・frozen id）→ Conventions → References(`(reviewed YYYY-MM-DD)`) → multi-session policy）。
- **skip**: macOS `.app`/codesign/TCC/launchd/`package.sh`/`setup-signing-cert.sh`/`stop.sh`（CLI には不要）・MCP/plugin（§4）。
- config 駆動: chord は `config.toml`（`#:schema ./config.schema.json` 行・全項目コメントアウトで「何も発火しない」既定）+ `config.schema.json`（draft-07・コード内 descriptor が authority・`chord config --emit-schema` で生成・drift test）+ swift-toml-edit を 1 shim 経由。furrow は `config.toml` テンプレ（repo root・read-only）+ `furrow schema` で index schema emit。

詳細な生成コード（marshal.go / task.go / fsstore.go / cliff.toml / commit-msg / run.sh / build.sh / .goreleaser.yaml / config.toml / CLAUDE.md / dependabot.yml / .editorconfig / furrow.index.v1.json）は workflow 出力に保存済（`tasks/w53x32tmg.output`）。実装の青写真として使用。

---

## 11. migrate の仕様（facet Task.md 169 行を実読・2026-06-25）

facet `Task.md` は **1 ファイルに [Open 優先順リスト + 要設計/triage + 温存 + Done 折りたたみ + 設計付録 A/B + プロセス規則 + [[wikilink]] + file:line refs] が同居** ＝ furrow が解消する痛みそのもの。`furrow migrate ./Task.md` のパース規則:

- **`## <emoji> <Lane>`** → status レーン（絵文字→status は config で写像。`🎯 Open`→ready/in-progress・`🔬 要設計/triage`→backlog・`🧊 温存`→icebox・`✅ Done`→done）。
- **`### N. title （…annotations）`**（Open 節）→ task。先頭 `N.` ＝ **priority 順**（`N×step`）。`（旧 R13）` 等は legacy alias（refs か body に温存）。
- **`- **R7. title** — body`**（triage/温存 節の箇条書き）→ task（別書式）。
- **`<details><summary>` 内の `- **title**（PR/commit）— #334`**（Done アーカイブ）→ done task。
- **`## 📎 付録 A/B`** → 該当 task の **body**（`bodies/<id>.md`）へ（名前一致で紐付け）か `docs/` へ。
- **`[[wikilink]]`** → 凍結 id へ解決（未解決はそのまま温存し lint で報告）。
- **`[file.swift:115](path#L115)` / URL** → `refs`。
- **`📌 …（暗黙にしない）`** callout → follow-up task or checklist item。
- 冒頭の `> 正本… 進め方…` プロセス規則 → tasks でなく CLAUDE.md / docs へ。
- `--dry-run` 既定（差分プレビュー）。**Phase 5 は最難関**（MEMO §7 既述）。

---

## 12. 追記ログ

- 2026-06-25: 初版。build-vs-buy / 命名 / ストレージ / 連携 / UI / 参考リポ / 工数を記録。
- 2026-06-25: Phase 1 完了。study-engine（§8）追加。家風リポ並列解析の結論（§10）・migrate 仕様（§11）を確定。実装に着手。
- 2026-06-25: Phase 0/2/3/4 実装。core（決定論マーシャラ・golden）・config（clamp）・store（fsstore/memstore）・app（coordinator）・CLI（15 コマンド）・schema・家風 scaffolding（cliff/commit-msg/CI/GoReleaser/flake/docs/CLAUDE.md）。`go test ./...` green。
- 2026-06-25: Phase 5 migrate（facet Task.md → 22 tasks・dry-run 既定・付録 skip・wikilink 温存）・Phase 6 TUI（bubbletea v1・list+glamour detail・done/move/edit/filter）実装＆merge。Phase 7 packaging を goreleaser/git-cliff 導入で検証（`brews`→`homebrew_casks`・`checksums`→`checksum` の設定バグ修正）。
- 2026-06-25: 全コードを **adversarial review workflow**（6 次元 reviewer → 独立 verify）に通し、確認された **10 件の実バグを修正**（Labels/Deps 非 dedup・laneRank sentinel 衝突・check 範囲外が exit 0・archive が index commit 前に body 削除・archive/migrate `--json` の null・bare-URL が CJK を飲む・md-link title 属性重複・list 節内 `###` が task を飲む・TUI reload の list blank/選択喪失）。各々に回帰テスト追加。`go test ./...` + golangci v2 = 0 issues。残: Phase 7 実リリース+nix vendorHash・Phase 8 web・migrate 付録 fold/wikilink 解決・TUI checklist/reorder。
- 2026-06-25: 改善 7 トピック（TUIラグ・Claude向けエージェント機能・スケジューラ・GTDレビュー・レーン構成・看板TUI・doc整理）を read-only リサーチし、**projects リポに t-0010〜t-0017 として起票**（運用タスクは projects に一本化済）。dependabot 5 件を triage：golangci-lint-action v9 / setup-go v6 をマージ、git-cliff v4 / goreleaser v7 は green（release.yml 変更ゆえ UI マージ待ち）、gomod group #5 は **Go ≥ 1.24.2 要求 + charm v1 系（bubbles/glamour）破壊的**で保留 → t-0016。**t-0010 TUIラグ修正を実装**（選択変更ごとの同期 `LoadBody`+`glamour.NewTermRenderer` 毎回生成が原因 → レンダラ&本文をキャッシュ・PR #6 マージ）。ROADMAP を「設計・決定の正典」として圧縮整理（フェーズ計画→現況表、残作業は projects を指す）。
- 注記（依存の現況）：§5 / §10 は依存計画で bubbles v1.0.0 / bubbletea v1.3.10 を想定したが、**実出荷は bubbles v0.20 / glamour v0.8 / lipgloss v1.0 / bubbletea v1.3.4**（go.mod 現況）。charm v1 系（Go 1.24 要求）への移行は **projects t-0016** で追跡（dependabot #5 はそれまで保留）。
