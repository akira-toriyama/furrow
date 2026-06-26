# furrow — ROADMAP（設計・決定の正典）

> **furrow** = repo の中に住むプレーンテキスト・タスクトラッカー。JSON index + 1 タスク 1 markdown 本文を、CLI と bubbletea TUI から操作する。私（トミー）と Claude Code の両方が編集できることを最優先に設計する。畝（furrow）を一本ずつ進めるようにレーンを消化する。
>
> **このファイルの役割** = 要件・確定した設計判断・フェーズの現況。調査の根拠は [`MEMO.md`](MEMO.md)、実装の作法・不変条件は [`CLAUDE.md`](CLAUDE.md)。**運用タスクは private な `akira-toriyama/projects` リポ（label `furrow`）に一本化**（この repo の自前 `.furrow/` board は廃止＝二重管理回避）。**未達成を暗黙にしない**。

---

## 背景と要件

GitHub Projects #5（private・106 items）+ Issues + facet の手管理 `Task.md`（169 行）でタスクを追う痛み——Issue は public で私的メモを置きづらく他人の issue と混ざる、`Task.md` は 1 ファイルに一覧+設計付録+プロセス規則+経緯が同居し優先度の手リナンバリング/手アーカイブが苦行——を、**ローカル・プレーンテキスト・自分仕様**の Go ツールで解消する。build-vs-buy の調査結論と却下理由は [`MEMO.md §1`](MEMO.md)。

**ハード要件（トミー指定）**: Claude Code / CLI フレンドリー・TUI 必須・**非バイナリで git-diff 可能**（GitHub Issue 連携は不要）・構造化メタ=JSON / 長文=Markdown / 設定=TOML・データ量は Projects #5 程度・完了は残す→後で archive・優先度は CLI/TUI 高 / web 低（将来 React）・brew/nix 配布。**進め方**: 1 セッションで完了しなくてよい / 破壊的変更OK / 品質重視 / 未達成を暗黙にしない。

---

## 確定した設計判断（根拠は MEMO.md）

1. **ストレージ=ハイブリッド**：`.furrow/index.json`（構造化メタ・機械が書く）+ `.furrow/bodies/<id>.md`（長文 prose・手/Claude 編集可）。純 JSON 単一 / JSONL は長文を 1 行エスケープに潰し git が全行 churn＝痛みの再現。（MEMO §3）
2. **index=JSON / 設定=TOML（`config.toml` 駆動の家風）/ 本文=Markdown**。YAML 却下（空白依存で編集が壊れる）・SQLite 却下（バイナリ・非 diff）。
3. **ID 凍結**（`t-k3m9p`・ローカル乱数 / 共有カウンタなし＝並行 add でも衝突しない）：再利用・リナンバリングしない。旧 numeric id（`t-0042`）も共存。
4. **priority=独立した疎な整数**（10 刻み）：並べ替え=1 フィールド編集＝手リナンバリング消滅。
5. **status=フィールド**（レーン定義は `config.toml` 駆動）：Open→Done は値変更＝手アーカイブ消滅。
6. **決定論シリアライズ**：`core.Marshal` を唯一の経路に。キー順固定・2-space・`SetEscapeHTML(false)`・`[]` not null・`lane→priority→id` sort・UTC 秒・末尾改行。アプリ書き込み＝手/Claude 編集が byte 一致＝git churn ゼロ。（MEMO §10 / CLAUDE.md）
7. **Claude 連携=MCP も plugin も作らない**（solo には過剰）。`CLAUDE.md` の連携ブロック＋全 read コマンドに `--json` が連携層。（MEMO §4）
8. **非対話デフォルト**（TTY 検出・TUI は `furrow ui` のみ・破壊操作は `--yes`）＋ **exit code 契約**（0 ok / 1 not-found・empty / 2 bad-usage・validation / 3+ 内部・IO、エラーは stderr の `{"error":{"code","id","message"}}`）。
9. **アーカイブ**：done はホット index に残し、`furrow archive --older-than 30d` で `.furrow/archive/` へ退避し軽さを保つ。

**スキーマ**：`schema_version:1`・フィールドは id/title/status/priority/labels/parent/deps/refs/checklist/created/updated/closed/body。正本は **`internal/schema.IndexV1`**（`furrow schema` で emit、`docs/schema/furrow.index.v1.json` に committed、CI が drift 検出）。不変条件：index の id ⇔ `bodies/<id>.md` が 1:1（`furrow lint` で検証）。

---

## フェーズ現況

| Phase | 内容 | 状態 |
|---|---|---|
| 0 Setup | repo / CI / commit 規約 / 家風 scaffolding | ✅ |
| 1 Study | 参考リポ調査・study-engine・migrate 仕様確定 | ✅（MEMO §8 / §10 / §11） |
| 2 Design lock | スキーマ・`config.toml`・determinism・hexagonal | ✅（[docs/architecture.md](docs/architecture.md)） |
| 3 Core lib | core / store / config / app・golden 往復テスト | ✅ |
| 4 CLI | add/ls/show/next/edit/done/move/reorder/check/archive/lint/init/schema/version/migrate/ui | ✅ |
| 5 migrate | `furrow migrate`（dry-run 既定・付録 skip+warn・`[[wikilink]]` 温存） | ✅（付録 fold / wikilink 解決は **won't-do**＝skip+warn が最終仕様） |
| 6 TUI | bubbletea v1・list + glamour detail・done/move/edit/filter・レンダラ/本文キャッシュ・checklist toggle（space）・reorder（K/J） | ✅（projects **t-0002** 完了＝checklist toggle / reorder の両方） |
| 7 Packaging | GoReleaser / brew cask / nix flake を設定 | 🟡 実リリース検証=projects **t-0001**・nix vendorHash=**t-0005** |
| 8 Web/React | read-only ビューア → React UI（host は Electron vs Go 静的を再検討） | 🧊 projects **t-0006 / t-0007** |

**残作業・新規アイデアは全て `akira-toriyama/projects`（label `furrow`）で追跡**（TUIラグ修正・エージェント機能・GTDレビュー・看板TUI・waiting レーン・スケジューラ・charm v1 移行・メタ情報設計 = t-0010〜t-0017 ほか）。このファイルは設計記録として維持する。

---

## 決定ログ（解決済みの論点）

- 短い alias `fw`：**不要**（2026-06-25）。欲しければ shell alias。`ls`→`list` の alias のみ維持。
- 既定レーン：Projects #5 の 6 段 `inbox/backlog/ready/in-progress/done/icebox` を採用。`config.toml [lanes].order` で切替可。※GTD の Waiting-For 用 `waiting` レーン追加を projects **t-0014** で検討中。
- `next` の意味：`config.toml [next].lanes` で対象レーン指定可（既定 `ready`+`in-progress`、両方無ければ非 terminal 全レーンにフォールバック）。deps-done 判定は従来通り。（実装済）
- `--field`：**不要**。`--json` + jq（例 `furrow ls --json | jq -r '.[].id'`）で代替。
- time 表示：index は RFC3339 UTC（決定論）。`show` の人間向け表示も UTC `2006-01-02 15:04`。
- `[labels].required`：任意・既定 off。on で label 無しタスクは `add` / `lint` でエラー（projects は on 運用）。
