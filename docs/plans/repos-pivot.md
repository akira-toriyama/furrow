# repos-pivot — furrow を GitHub Projects/Issues の「代替」へ（設計 v1.1）

> **状態（2026-07-02）**: 設計はユーザ承認済み（①〜⑧全節＋3視点敵対レビューの指摘一式を v1.1 として採用）。**タスクは projects 中央ボードに起票済み**（下表）— 各 body に該当節を verbatim 転記済みなので、**実装セッションはまず `furrow show <id>` を読む**こと。本ファイルは全体像（順序・依存・横断の理由）の正本。全タスク完了（flag-day #2 終了）で削除する。

| # | task id | 内容 | dep |
|---|---|---|---|
| P0 | `t-6fp3` | 自リポの `.furrow/` tombstone 削除（central board へ一本化） | — |
| P1 | `t-tnjc` | core: `repos` 一級フィールド＋schema v2＋version gate | — |
| P2 | `t-xvm6` | cli: `-r`・`furrow repo`・drafts・`no_repo`・did-you-mean・candidates・表示 | P1 |
| P3 | `t-14sw` | config: `repo="auto"` 導出（origin INI・worktree commondir）＋label literal 化＋strict write | P1 |
| P5 | `t-jkck` | `furrow sync`（gitrepo adapter・失敗契約込み） | — |
| P8 | `t-cckp` | README/docs ポジショニング転換 | P2,P3,P5 |
| P6 | `t-jhv6` | release v0.4.0（repos 対応）＋flake vendorHash | P8 |
| P7 | `t-epcx` | task-status reusable workflow 同梱・公開＋CI バイナリ pin | P6 |
| P4 | `t-3bmm` | flag-day #2: 194 件 labels→repos 変換・meta 3・運用ルール/dotfiles（**user gate**） | P7 |

## Context（なぜ）

furrow は GitHub Projects/Issues への不満（issue は clone できず agent フレンドリーでない・進捗と剥離する）から生まれたが、当初は個人タスク管理想定でマルチリポは後付けだった。方針転換（2026-07-02 確定）:

- **GitHub Projects/Issues の代替**を目指す（完全互換ではなく代替。プレーンテキスト＝clone できることが最大の優位）。
- **リポジトリを labels の間借りから一級概念へ**: task は 0..N 個の repo に関連づく。repo なし task ＝ issue draft 相当を正式サポート。
- **GitHub Actions を furrow 公式として提供**（既存の task-status 同期の公式化）。
- **N リポ × N マシン × N ボード**を現実的な範囲でケア（projects を PC A/B で clone して動くこと）。

土台は 7〜8 割済み: central board（#36–#38）・shard 化による並行書き込み無衝突（t-jg4q）・task-status Action の私物実装。転換の本質は「意味づけの昇格と公式化」。

## 確定した設計判断

1. repo 識別子は **owner/repo 形式**。CLI は一意なら `-r furrow` の短縮可（case-insensitive・`/` 境界 suffix 一致）。
2. Actions はまず **task-status 同期の公式化のみ**。
3. マルチマシン同期は **`furrow sync`**（git の薄い wrapper。daemon/cloud なし＝non-goals 維持）。
4. **破壊的変更 OK・負債0**（互換レイヤなし。version gate と `label="auto"` tombstone 警告は負債でなく保険）。
5. board の `label` は **literal 専用タグとして残す**（`"auto"` の意味は新設 `repo` キーへ）。
6. assignee 概念は今回なし。

## 設計の要点（詳細は各 task body に verbatim）

- **① データモデル（t-tnjc）**: `Task.repos []string`（labels と同じ set 意味論）。schema doc は **v2 へ bump**（v1 削除・golden 2本更新）。**version gate**: meta.json の version が自バイナリ超なら Load/Save 拒否（exit 3）— 旧バイナリの lenient unmarshal が repos を黙って剥がす事故を機械的に封じる。
- **② 導出（t-14sw）**: `repo="auto"` は `.git/config` の `[remote "origin"]` 先頭 url を INI section-aware にパース（scp 風/ssh://\/https 対応）。worktree は gitdir→commondir 追跡（**dir 名ズレの既知問題が解消**）。fallback は ghq パス→**失敗時は draft＋警告（bare 名を書かない不変条件）**。`label="auto"` は予約語警告。pointer は `default_repo` へ。
- **③ CLI（t-xvm6）**: `-r` フィルタ・`furrow repo` コマンド・`ls --drafts`/`add --draft`・revisit `no_repo` シグナル・**did-you-mean ガード**（旧習慣 `-l <repo>` の silent empty を exit 2＋candidates で受け止める）・error 封筒に optional `candidates`・`-l`/`-r` 直交・表示面（show/ls/diff/TUI）。
- **④ sync（t-jkck）**: `.furrow/` pathspec 限定 auto-commit → autostash pull --rebase → push（1 リトライ）。conflict は自動 abort→`sync-conflict` エラー（パス一覧入り JSON）。進捗オブジェクトは失敗時も stdout。
- **⑤ Actions（t-epcx）＋ release（t-jhv6）**: reusable workflow を furrow 同梱・**具体 release tag 参照**（moving `@v1` は GoReleaser tag 空間と衝突）・バイナリは自 tag 一致の release 資産 DL。release は v0.4.0 仮置き（v0.1.0〜v0.3.0 は既存 — docs の「未タグ」は stale、P8 で修正）。
- **⑥ flag-day #2（t-3bmm）**: repo ラベル→`repos` 変換・**全 shard に `repos:[]` 実体化**・meta 2→3。順序: バイナリ更新（**CI 含む**＝第3の書き手）→書き込み停止→コピーでリハーサル→単一 commit→変換器破棄。
- **⑦ docs（t-cckp）**: 一枚看板を「Projects/Issues の代替」へ。non-goals は維持しつつ MCP/plugin の**根拠文**を書き直す（"single-repo/single-author" が偽になるため）。glossary の label=auto 焼き付き 4 項も更新。

## 進め方

1 item = 1 PR（squash・docs 同梱）・TDD・`sh scripts/check.sh` 緑必須。実装は task ごとに別セッション＋worktree。P0/P1/P5 は ready（並行可）、以降は dep 順。P4 は PR でなくデータ作業（user gate・書き込み停止を伴う）。

関連: 旧提案 T1–T5（review walker / revisit 拡張 / context export、`projects` の backlog）は本転換と直交で存続。T2 の review walker は draft ステップ（`no_repo` と合成）を含める余地あり。
