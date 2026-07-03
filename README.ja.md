# furrow

> English: [README.md](README.md)

**furrow** は **GitHub Projects / Issues の代替**——clone できる git ネイティブなプレーンテキスト・タスクトラッカー。Go 製の単一バイナリで、構造化メタデータを 1 タスク 1 つの JSON シャード `.furrow/tasks/<id>.json` に、長文の散文を `.furrow/bodies/<id>.md` に分けて持つ。ユーザとコーディングエージェントの両方が、git で綺麗に diff できる素のテキストとしてトラッカーを編集できることを最優先に設計している。畝（furrow）を一本ずつ進めるように、レーンを消化していく。

- **module**: `github.com/akira-toriyama/furrow`
- **Go**: 1.25+

---

## なぜ Issues でないのか

Issues への不満は単純である。**issue は clone できない**——プレーンテキストなら clone でき、オフラインで動き、コードと一緒に grep できる。エージェントは API クライアントなしで、普通のファイル操作と CLI だけでトラッカーを**読み書き**できる。そしてトラッカーが作業と同じ git に住むので、**status が実作業から剥離しない**——コードを変える push がタスクも変えられる。書き込みはバイト安定なので、`git diff` には実際に変わったものしか出ない。

furrow はこれを Go で実装する。外部サービス連携は持たず、git repo に commit して綺麗に diff できれば十分、という割り切りである。furrow が意図的に「やらないこと」とその理由は [`docs/non-goals.md`](docs/non-goals.md) にまとめてある。

## 使い方は二通り

- **中央ボード** —— clone できる 1 つのトラッカー repo が**全 repo** を背負う。各タスクは関連 repo を一級の `repos` フィールド（`owner/repo`）で持ち、各 checkout は自分の repo に自動スコープされ、複数マシンの clone は `furrow sync` で収束する。これが GitHub Projects 代替のモード——[中央ボード](#中央ボード)を参照。
- **リポローカル** —— もう一つの使い方: 1 つの repo がコードの隣に自前の `.furrow/` を持つ（`furrow init` するだけ）。従来のモードで、今も完全サポート。以降のクイックスタートはこの形で進める（ボードのスコープ以外は中央ボードでも同一に動く）。

---

## ストレージ（ハイブリッド）

ストアは repo 内の `.furrow/` ディレクトリ一つに収まる。

```
.furrow/
  tasks/
    t-0001.json     # 1 タスク 1 つの構造化メタデータシャード（機械が書く・決定論シリアライズ）
    t-0002.json
  bodies/<id>.md    # 1 タスク 1 つの長文 markdown 本文（人/エージェントが編集可）
  meta.json         # ボード全体のレイアウト版（{"schema_version": 3}）
  config.toml       # 人が編集する設定（furrow からは READ のみ）
  archive/          # 退避した古い done タスク（独自の tasks/ + meta.json + bodies/）
```

役割分担はこうなっている。

- **`tasks/<id>.json`** = 構造化メタデータだけ、1 タスク 1 ファイル。小さく、`jq` や Go で即クエリでき、フィールド単位で diff できる。**唯一の決定論マーシャラ（`core.MarshalTask`）からしか書かれない。**
- **`bodies/<id>.md`** = 素の markdown。エスケープなし、タスク単位で diff できる。**手でも Claude でも自由に編集してよい。**
- **`meta.json`** = ボード全体のレイアウト版（`{"schema_version": 3}`）だけを持つ専用ファイル。**シャードの中には決して入れない**ので、版を上げても触るのは 1 ファイルだけで、どのシャードも git のマージ点にならない。
- **`config.toml`** = 人が編集する設定。furrow は書き換えず、READ するだけ。
- **`archive/`** = 古くなった done タスクの退避先（独自の `tasks/` + `meta.json` + `bodies/` を持つ兄弟シャードストア）。

かつては単一のソート済み JSON 配列だったが、2 人の作業者が別々の worktree／PR でタスクを追加・編集すると git のマージで衝突していた。1 タスク 1 ファイルなら、別々の id は別々のファイルに触れるので、git のマージは衝突なしの union になる（`bodies/<id>.md` は元から per-file で衝突なし——この設計はその利点をメタデータへも広げる）。長文を 1 行のエスケープ文字列に潰す純 JSON 単一ファイルや JSONL を避け、メタと本文を分離するのがこの設計の肝である（詳しい理由は [`docs/non-goals.md`](docs/non-goals.md)）。

### 画像・メディアの添付

タスク本文は素の Markdown なので、スクショや図はファイルを bodies の隣に commit し、**相対パス**でリンクすれば添付できる。

```markdown
![repro](assets/t-0001-bug.png)
```

Markdown が描画される場所（GitHub・Obsidian・エディタのプレビュー）では表示されるが、**端末では表示されない**（`furrow ui`/`show` は絵ではなくテキストを出す）。furrow 自体はこれらのファイルを特別扱いせず、ただの repo の一部として扱う。実務上の注意：

- スクショは小さく保ち、秘匿情報はマスクする（git 履歴は永久）。
- **private** repo では、画像を repo 内に commit して相対リンクするのが確実（外部/raw の画像 URL は認証が要り失効する）。public repo なら外部ホストへのリンクも可。
- 動画など大きいメディアは、**最初の 1 つを commit する前に** Git LFS（`.gitattributes`）で track する（後から LFS を入れても効くのは新規ファイルだけで、既存 blob の掃除には履歴書き換えが要る）。

### 決定論（生命線）

各シャードの書き込みは `core.MarshalTask` という**唯一の経路**を通る。契約は以下のとおり。

- key 順 = struct のフィールド宣言順
- 2-space インデント
- `SetEscapeHTML(false)`（CJK や `< > &` がそのまま残る）
- 空のコレクションは `null` でなく `[]`
- label / dep 集合はソート＆重複除去
- タイムスタンプは UTC・秒単位（RFC3339 の `...Z`、ナノ秒なし）
- 末尾改行あり

この結果、**`furrow` が書いたバイト列と、人や Claude が手編集したバイト列が一致する**。しかも Save はバイト列が変わったシャードだけを書くので、no-op の保存では git の churn はゼロになる。

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

配布は GoReleaser から Homebrew tap（`akira-toriyama/homebrew-tap`）と nix flake へ流す。`v0.1.0`〜`v0.6.1` が公開済み。nix flake の `vendorHash` は `v0.4.0` で実 hash 化済み（`flake.lock` も commit 済み）。

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
| `init` | カレントディレクトリに `.furrow` ストアを作る（`config.toml` + `meta.json` + 空の `tasks/` + `bodies/`） |
| `add <title>...` | タスクを追加（`--stdin` で標準入力から1行1タスクを一括作成）。id を自動採番し `bodies/<id>.md` を作る |
| `ls`（別名 `list`） | タスクを正準順で一覧。`--drafts` で repo 未付与のタスク（draft）だけを一覧（ボードのスコープは無視） |
| `show <id>` | タスクを markdown 本文付きで表示。`--backlinks` を付けると、本文でこのタスクを `[[id]]` で参照している他タスクも列挙する（「Mentioned in」節／`--json` では `mentioned_by` 配列。GitHub の "mentioned in" のローカル・レート制限なし版） |
| `next` | 着手可能なタスク（非 terminal・依存が全部 done）を表示。`--json`/`--ndjson` は各タスクに `reason`（`in_next_lane`・`deps_satisfied`）を付与 |
| `revisit` | read-only。再評価すべき open タスクを一覧。`--json`/`--ndjson` は各タスクに `revisit` 配列 `{code, detail}`（`no_repo`・`value_unset`・`effort_unset`・`stale`・`dep_done`）を付与し、エージェントが何を直すか分かる。draft はスコープに関係なく浮上する。空でも exit 0。`-l/--label`・`-r/--repo`・`-n/--limit`・`--stale-days <n>`（0 で stale 無効） |
| `edit <id>` | `bodies/<id>.md` を `$EDITOR` で開く（非対話ならパスを出力） |
| `done <id>` | done レーンへ移動し `closed` を打刻 |
| `move <id> <lane>` | 任意のレーンへ移動 |
| `reorder <id> <priority>` | priority（疎な整数）を設定 |
| `retitle <id> <title...>` | タイトルを変更。シャードの title **と** body 先頭の `# ` 見出しを両方更新して食い違わせない（末尾の引数は空白で連結するのでクォート不要） |
| `value <id> <1-5>` | 粗い value（重要度）見積もりを設定（範囲外は 1..5 に丸め）。`--clear` で未設定に戻す |
| `effort <id> <1-5>` | 粗い effort（手間）見積もりを設定（1..5 に丸め）。`--clear` で未設定に戻す |
| `check <id> [index]` | チェックリスト項目をトグル（`--add` で追加・`--off` で外す） |
| `dep <id> <dep-id>` | 依存を追加（id が dep-id を待つ）。`--rm` で削除。循環防止・冪等 |
| `label <id>` | ラベルを追加／削除（`--add`・`--remove`、いずれも反復可・併用可）。冪等 |
| `repo <id>` | repo（`owner/repo`）を追加／削除（`--add`・`--rm`、反復可・併用可）。値は完全な `owner/repo` か、ボード既知の repo に一意に解決する短名のみ（それ以外は exit 2・`candidates` 付き）。冪等。repos が空のタスクは draft |
| `apply` | PR/コミット本文から `SetStatus-task: <body-link> [<lane>]` ディレクティブを解析して適用（stdin または `--body-file`）。status 自動更新の CI フック。`--on open` は in-progress へ寄せ、`--on merge` は lane を適用。検証は非ブロッキング |
| `sync` | マルチマシン運用の儀式を 1 コマンドで: `.furrow/` 限定の auto-commit → `pull --rebase`（autostash）→ `push`（non-fast-forward 時は pull→push を 1 回リトライ）。conflict 時は自動 abort（`sync-conflict` エラーにパス一覧）。進捗 `{committed, pulled, pushed, conflict}` は失敗時も stdout に出る |
| `archive` | 古い done タスクを `.furrow/archive/` へ退避（`--yes` なしはプレビュー） |
| `lint` | shard↔body の整合・レーン・依存・config を検査（依存の循環は error、存在しない id への `[[id]]` リンクは warn＝archive 済み id は dangling 扱いしない。書きかけのユーザー設定の clamp 警告も含む） |
| `config init` | ユーザー設定 `~/.config/furrow/config.toml`（中央ボード雛形）を書き出す。ボード内で実行すると最寄りの `.furrow` から path/scopes を文脈導出、離れていればコメント付き placeholder。既存ファイルは上書きしない（`--path`・`--scope`（複数可）） |
| `config path` | 解決されるユーザー設定パスを表示。書きかけ設定の clamp 警告は stderr へ（stdout は path のみ） |
| `schema [task\|meta]` | JSON Schema を出力（引数なし or `task` = シャード（`tasks/<id>.json`）のスキーマ・`meta` = `meta.json` のスキーマ） |
| `version` | furrow のバージョンを出力（stamp 済みならビルド commit/date も）。root の `--version` フラグでも同じ行を出力。`--json` は `{version, commit, date, modified}` を出力（スクリプト／エージェント向け） |
| `ui` | 対話 TUI を起動（一覧＋詳細ペイン：移動・フィルタ・done・レーン移動・並べ替え（`K`/`J`）・チェックリストトグル・本文編集） |
| `migrate <file>` | 既存の `Task.md` などを取り込む（dry-run 既定／`--write` で作成・未対応の見出しや `[[wikilink]]` は破棄せず報告） |

### 主なフラグ

- `--status, -s <lane>` — レーンで絞り込み（`ls`）
- `--label, -l <label>` — ラベル（純粋なタグ）で絞り込み（`ls`/`next`/`revisit`。スコープと AND）。`add` では `-l` 繰り返しでラベルを付与
- `--repo, -r <owner/repo|短名>` — repos フィールドで絞り込み（`ls`/`next`/`revisit`）。短名は `/` 境界で大文字小文字を無視して解決（`-r furrow` → `akira-toriyama/furrow`。曖昧なら exit 2・`candidates` 付き）。明示 `-r` はボードのスコープを上書きし、`-r ''` で全件。`add` では `-r` 繰り返しで repo を付与
- `--drafts`（`ls`）/ `--draft`（`add`） — draft（repo 未付与タスク）だけを一覧／draft として作成（`--draft` は `-r` と併用不可）
- `--limit, -n <N>` — 行数の上限（`ls` / `next`。`0` は全件、`next` は `-n1` で先頭だけ）
- `--priority, -p <N>` — `add` で priority を明示（省略時はレーン末尾に追記）
- `--value <1-5>` / `--effort <1-5>` — `add` で粗い value/effort 見積もりを付与（範囲外は 1..5 に丸め・省略で未設定）
- `--parent <id>` / `--dep <id>`（繰り返し）/ `--ref <file:line|URL>`（繰り返し）/ `--body <md>` — `add` のメタ指定
- `--add <text>` / `--off` — `check` のチェックリスト操作
- `--older-than <days>` / `--yes` — `archive`（`--yes` なしは dry-run プレビュー）
- `--on open\|merge` / `--ref <src>` / `--body-file <path>` / `--open-lane <lane>` — `apply`（`--on` 必須。`--ref` は本文に記録する出典 例 `furrow#42`）
- `-m/--message <msg>` — `sync` の auto-commit メッセージ上書き（既定 `:card_file_box: chore(board): sync via furrow`）

`add` の `--status` 省略時は `config.toml` の `[lanes].default`、`archive` の `--older-than` 省略時は `[archive].older_than_days` が使われる。

---

## CLI 契約（Claude Code / スクリプト向け）

furrow は **非対話がデフォルト**。プロンプトは出さない（TTY 検出は `golang.org/x/term`）。対話 UI は `furrow ui` だけ。

- **`--json`** — read コマンドが JSON を **stdout のみ**に出す。ログ・エラーは stderr へ。
- **`--ndjson`** — タスクを 1 行 1 JSON で出す（list 系）。
- **フィルタ** — `--status/-s`・`--label/-l`・`--repo/-r`・`--limit/-n`。明示 `-l X` が 0 件で、X がタスクを持つ repo 短名に一意解決するときは exit 2 で `-r X` へ誘導する（did-you-mean ガード）。明示 `-r` が draft を隠したときは stderr に `N draft(s) hidden — furrow ls --drafts` を 1 行出す。
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

  入力が「あと一歩で解決できた」とき（repo 短名の曖昧・ラベルが repo を一意に指す did-you-mean ガード）は、封筒に `"candidates": ["owner/repo", …]` も載る。スクリプトはメッセージ文をパースせず、この配列から選べばよい。

JSON 出力例:

```sh
furrow ls --json | jq '.[] | select(.status=="ready") | .id'
furrow next --ndjson
furrow show t-0001 --json   # task + body_text
```

ストアの場所は、カレントから親方向に `.furrow/` を探して解決する。`FURROW_DIR` で明示も可能。`edit` のエディタは `FURROW_EDITOR` → `VISUAL` → `EDITOR` → `vi` の順で選ぶ。

### 中央ボード

GitHub Projects 代替のモードがこれ。複数 repo で 1 つの中央ボード（clone・grep・diff できる
横断トラッカー repo）を共有し、各 repo をその repo
（`owner/repo`＝一級の `repos` フィールド）へ自動スコープできる。repo 群まとめて一度に
（user-level config）か、repo ごと（pointer ファイル）の 2 通り。

#### user-level config（per-repo ファイル不要）

repo 群に対し **設定ゼロ**で 1 つ以上の中央ボードを既定にする（新規 repo も自動でカバー）。
`furrow config init` で雛形を出す（中央ボードの repo 内で実行すると path/scope を埋めてくれる。
離れた場所ではコメント付き placeholder を書く）か、`~/.config/furrow/config.toml`（または
`$XDG_CONFIG_HOME/furrow/config.toml`）を手で書く。場所は `furrow config path` で分かる:

```toml
[[board]]
path        = "~/src/github.com/me/projects/.furrow"  # 中央 .furrow（~・本ファイル基準の相対・絶対）
scopes      = ["~/src/github.com/me"]                 # ここ配下でだけ有効（最低 1 つ必須）
repo        = "auto"                                  # "auto" = checkout から owner/repo を導出 / "" = 無 / リテラル "owner/repo"
label       = ""                                      # 任意: `add` が付ける純リテラルタグ（読み取りは絞らない）
auto_filter = true                                    # ls/next/revisit をボード repo で絞る（既定 true / false = ボード全部）
```

**cwd が `scopes` のいずれか配下のときだけ**有効で、それ以外では furrow は無設定時と完全に
同じ挙動。`[[board]]` を並べれば別ツリーを別ボードへ振り分けられる。cwd を複数の scope が
含む場合は**最も内側（最長一致）が勝つ**（同長は記述順で先勝ち）。`scopes` の無いボードは
推測せず無視されるので、書きかけのエントリが他所で furrow を壊さない —— そのぶん黙殺になるので、
clamp された内容は `furrow lint` と `furrow config path` が報告する。

`repo = "auto"` は最も近い git checkout からスコープ repo を導出する —— ファイル読みのみで
`git` は起動しない：`.git/config` の `[remote "origin"]` の**先頭の `url` 1 行だけ**を
INI として読む（scp 風 `git@host:o/r.git`・`ssh://`・`git+ssh://`・`http(s)://`、`.git`
有無どちらも可。`pushurl`・2 行目の `url`・他の remote は決して見ない）。worktree の
`.git` **ファイル**は `gitdir`→`commondir` を辿って共有 config に届くので、`chord-fix-y`
という名の worktree でも `owner/chord` が導出される。origin が使えなければ ghq 風パス
（`…/github.com/<owner>/<repo>`）に fallback し、それも無ければボードは**スコープ無し**で
開く（stderr に注記・`add` は draft を作る）—— 素の dir 名を `repos` に書くことは決してない。
git repo の外でも同様（ボードは開く・注記のみ）。`FURROW_BOARD=<path>` は単発・テスト用に
単一ボードで全体を上書きする（scope は board の repo 親）。廃止された `label = "auto"` は
警告付きで無視され、`repo = "auto"` へ誘導される。

#### per-repo pointer

特定の 1 repo は、直下の `.furrow-pointer.toml` で redirect できる（user-level の中央
ボードに**優先**する）:

```toml
board = "../projects/.furrow"   # 中央 .furrow（本ファイル基準の相対・~・絶対）
default_repo = "me/chord"       # 任意: 1 owner/repo にスコープ（"auto" で導出 / "" = redirect のみ）
```

#### 発見の優先順位

`FURROW_DIR`（明示・スコープ注入なし）→ 直近の親で `.furrow` を持つディレクトリ（実体の
ローカルストアが勝つ）→ `.furrow-pointer.toml`（ボードへ redirect）→ **user-level の中央
ボード**（cwd が `scopes` のいずれか配下のとき・最長一致が勝つ）→ `furrow init`。

ボード有効時（pointer / user-level いずれも）:

- `furrow add "…"` はスコープ repo を `repos` に union（明示 `-r x` は置換でなく追加）。
  `add --draft` はちょうどこの union だけを抑止する。ボードのリテラル `label` があれば
  従来どおりラベルに union。
- `furrow ls|next|revisit` はスコープ repo で絞る（**banner なし＝サイレント**）。
  user-level ボードは `auto_filter = false` で「絞らずボード全部を表示（`add` の repo 付与は
  維持）」に opt-out できる。pointer は常に絞る。スコープ制御は `-r`：`-r ''` でその場限り全件、`-r <repo>` で別 repo。明示 `-l tag` はスコープ**内**をタグで絞る（AND であり、スコープを外さない）。スコープが draft を隠したときは stderr に 1 行 `furrow ls --drafts` への hint が出る。

#### マルチマシン: `furrow sync`

中央ボードを複数マシン（PC A/B）で clone して使うときの規律は「読む前に pull・
書いたら push」だけ。`furrow sync` はそれを 1 コマンドにした **git の薄い wrapper**
（daemon や同期サーバーではない — [docs/non-goals.md](docs/non-goals.md)）:

1. `.furrow/` **pathspec 限定**の auto-commit（board repo にある他の dirty ファイル＝
   ノート類は巻き込まない）。既定メッセージ `:card_file_box: chore(board): sync via furrow`、
   `-m` で上書き
2. `git -c rebase.autoStash=true pull --rebase`
3. `git push`（non-fast-forward なら pull→push を 1 回リトライ）

shard 化により本当の conflict は稀（別マシンの add 同士は別ファイル）。**同じ** task を
両側で編集したときだけ conflict し、その場合 sync は **rebase を自動 abort** する
（board に conflict marker を残さない・ローカルの sync commit は残る）。exit 3 の error
封筒に `"id": "sync-conflict"` と `"details": {"paths": [...]}` が入るので、agent は
どの shard を手で直せばよいか機械的に分かる。進捗オブジェクト
`{committed, pulled, pushed, conflict}` は成功・失敗を問わず stdout に出る。

---

## 設計の不変条件

- **id は凍結**。`t-k3m9p` 形式（prefix + ランダムな Crockford base32 サフィックス、`[ids].width` 文字）。共有カウンタを持たずローカル生成するので、並行 `furrow add` でも衝突しない。**再利用も再採番もしない**。旧来の連番 id（`t-0042`）も有効で共存。`bodies/<id>.md` のファイル名語幹と 1:1 に対応する。
- **priority は疎な整数**（10 刻みが既定）。並べ替えは `reorder` で 1 フィールドを書き換えるだけ。手リナンバリングは消える。
- **status は `config.toml` のレーン**。Open→Done は値の変更（1 文字 diff）。
- **`done` への移動は `closed` を打刻**。done から外へ移動すると `closed` をクリアする。icebox（温存）のような他の terminal レーンは `closed` を打刻しない（parked と closed は別物）。
- **`next` の定義** = terminal でないレーン、かつ依存（`deps`）が全て done レーンにあるタスク。
- **shard ↔ body は 1:1**。`furrow lint` が、本文ファイルのないタスクと、タスクのない孤立本文の双方を報告する。

### スキーマ（`.furrow/tasks/<id>.json`）

```jsonc
{
  "id": "t-0042",                 // 凍結・bodies/<id>.md の語幹
  "title": "…",                   // 一行サマリ
  "status": "in-progress",        // config.toml のレーン
  "priority": 100,                // 疎な整数・並べ替えはこれだけ
  "value": 4,                     // 任意・粗い 1..5（重要度）。未設定なら省略
  "effort": 2,                    // 任意・粗い 1..5（手間）。未設定なら省略
  "labels": ["core", "cli"],      // 純粋なタグ（repo はラベルではない）
  "repos": ["akira-toriyama/furrow"], // 一級の repo 集合（owner/repo・0..N。空 = draft＝issue draft 相当）
  "parent": "t-0001",             // 任意（omitempty）
  "deps": ["t-0003"],             // 依存（next が ready 判定に使う）
  "refs": ["docs/x.md#L10", "https://…"], // file:line / URL
  "checklist": [ { "text": "…", "done": false } ],
  "created": "2026-06-25T00:00:00Z",
  "updated": "2026-06-25T00:00:00Z",
  "closed": null,                 // open の間は null・done で打刻
  "body": "bodies/t-0042.md"      // 相対パス（本文そのものではない）
}
```

ボード全体のレイアウト版は `meta.json` に独立して持つ（シャードには入れない）:

```json
{ "schema_version": 3 }
```

正準スキーマは `furrow schema [task|meta]` が出力する（draft 2020-12）。これが正本で、`docs/schema/furrow.task.v2.json` と `docs/schema/furrow.meta.v2.json` が commit 済みのコピー。CI が両者を diff して drift を防ぐ（`v2` はスキーマ**文書**の版号で、ボードのレイアウト版＝`meta.json` の `schema_version` は 3）。

`value` / `effort` は、エージェント（や自分）が「次に何をやるか」を毎回見積もり直すのではなく**記録済みデータから選ぶ**ための任意フィールド。**ROI = value ÷ effort は導出で保存しない**（どちらを直しても常に最新の ROI になり、古い数字が残らない）。`next` はあえて据え置き——ROI 並べ替えは呼ぶ側の選択：

```sh
# value/effort を両方持つタスクを value÷effort の高い順に
furrow ls --json | jq 'map(select(.value and .effort)) | sort_by(-(.value / .effort))'
```

`furrow revisit` はそのエージェント向けの相棒——**read-only** で、メタデータが古くなり得る open タスク（`value`/`effort` 未設定・`[revisit].stale_days` 以内に更新がない stale・既に done な依存を持つ）を洗い出す。各タスクには `revisit` 配列 `{code, detail}` が付くので、エージェントは既存セッター（`value`/`effort`/`dep`）で何を直せばよいか分かる。コマンド自身は一切変更しない。

```sh
# このリポで見積もりがまだ要るタスクを理由つきで
furrow revisit -r furrow --json | jq '.[] | {id, revisit: [.revisit[].code]}'
```

---

## アーキテクチャ（hexagonal）

ports & adapters。依存は内向きにのみ流れる。詳細図は [`docs/architecture.md`](docs/architecture.md)。

```
cmd/furrow/main.go                 = os.Exit(cli.Execute()) のみ
  └─ internal/cli   (cobra アダプタ)        ┐
     internal/tui   (bubbletea v1・対話 UI)         ┘ presentation
        └─ internal/app   (唯一の mutation funnel・CLI/TUI 共通)
              ├─ internal/config        (config.toml ロード・clamp-don't-reject)
              ├─ internal/store/fsstore (FS に触る唯一の package)
              ├─ internal/store/memstore (in-memory fake)
              └─ internal/gitrepo       (`furrow sync` 用の git subprocess アダプタ)
                    └─ internal/core  (純ドメイン・stdlib のみ)
```

- **`internal/core`** — 純ドメイン。`Index` / `Task` 構造体、唯一の `core.MarshalTask` 経路、`Store` / `Clock` などの port（interface）、validate、index 操作を持つ。**標準ライブラリしか import しない**（cobra・bubbletea・os・filepath は禁止）。
- **`internal/config`** — `config.toml` を読むだけ。clamp-don't-reject。
- **`internal/store/fsstore`** — **FS に触る唯一の package**。atomic write（同一ディレクトリの tmp + rename）、本文の lazy load、ランダム id 生成（`NextID`、共有カウンタなし）。
- **`internal/store/memstore`** — in-memory の fake（テスト・dry-run 用）。
- **`internal/gitrepo`** — `furrow sync` の背後にある git subprocess アダプタ（コマンド組み立て＋エラー分類だけの薄い wrapper）。`internal/app` からのみ駆動され、ストアのファイルには触れない（FS は fsstore の専権のまま）。
- **`internal/app`** — **唯一の mutation funnel**。CLI も TUI も必ずここを経由する。frozen id・正準順・closed 打刻・body↔shard の対応をここで一括管理する。
- **`internal/cli`** — cobra アダプタ。
- **`internal/tui`** — bubbletea v1 の対話 UI（`furrow ui`）。CLI と同じく presentation 層で、mutation は必ず `internal/app` 経由。
- **`internal/schema`** — JSON Schema のソース。`internal/version` — ビルドバージョン（リンカ注入。from-source は `dev`）。

---

## 設定（`.furrow/config.toml`）

`furrow init` がテンプレートを書き出す。furrow は **READ するだけ**で、書き換えない。方針は **clamp-don't-reject**：不明なキーは無視し、範囲外の値は安全な既定へ丸めたうえで `furrow lint` が警告する（タイプミスでツールが壊れない）。

```toml
[lanes]
order = ["inbox", "backlog", "ready", "in-progress", "waiting", "done", "icebox"]
default = "inbox"           # `furrow add` が割り当てるレーン
done = "done"               # `furrow done` の移動先（closed を打刻）
terminal = ["done", "icebox", "waiting"]  # `next` で着手不可とするレーン

[priority]
step = 10
default = 100

[ids]
prefix = "t-"
width = 5                   # ランダムサフィックスの文字数（例: t-k3m9p）

[archive]
older_than_days = 30

[revisit]
stale_days = 30             # `furrow revisit` が「更新なし」を stale 扱いする日数（0 で無効）

[ui]
theme = "auto"              # auto | dark | light
```

`[lanes].order` は status の enum 兼ソート順位を兼ねる。`NO_COLOR` は `[ui].theme` の値に関係なく常に尊重される。

---

## Claude Code 連携

furrow の連携層は意図的に薄い。**MCP も plugin も作らない**——素の CLI がそのままエージェント・インターフェースだからである: 全 read コマンドの `--json`/`--ndjson`、機械処理できるエラー封筒、そして clone できるプレーンテキストのストア（本文はエージェントが直接編集してよい）。daemon や第二のプロトコルを足しても、新しい能力は何も増えず運用面だけが増える（詳しい理由は [`docs/non-goals.md`](docs/non-goals.md)）。連携面は `CLAUDE.md` の短いブロックと `--json` だけ。

Claude（やエージェント）に守らせるルール:

- **`tasks/<id>.json` と `meta.json` を手編集しない。** 単一のマーシャラが所有しており、手編集は git の churn を生む。`add` / `move` / `reorder` / `retitle` / `done` / `check` などのコマンドで変更する（タイトル変更は `retitle`——シャードと body 見出しを一括更新）。
- **`bodies/*.md` は編集してよい。** 長文の散文はここに置く。
- 状態変更は必ずコマンド経由。出力を機械処理するなら `--json` / `--ndjson` を使う。

### CI: PR から tracker を自動更新

`furrow apply` はマージされた PR を status 更新に変える（furrow tracker 版の `Closes #N`）。
PR 本文にタスク本文ファイルを指す footer を 1 行入れる:

```
SetStatus-task: https://github.com/<owner>/<tracker>/blob/main/.furrow/bodies/<id>.md done
```

PR **open**（draft 含む）でタスクは in-progress に寄り、**merge** で指定 lane が適用される
（lane 省略なら本文に追記のみ）。`apply` は `--body-file` か stdin からテキストを読む CI/VCS
非依存の設計。

GitHub 側の配線は **furrow 同梱の reusable workflow**
[`.github/workflows/sync-task-status.yml`](.github/workflows/sync-task-status.yml) が担う。
利用側 repo は 10 行程度の caller を置くだけ — 参照は moving ref でなく**具体的な
furrow release tag に pin** する:

```yaml
# .github/workflows/task-status.yml
name: task-status
on:
  pull_request:
    types: [opened, reopened, ready_for_review, closed]
permissions:
  contents: read
  pull-requests: write
jobs:
  sync:
    uses: akira-toriyama/furrow/.github/workflows/sync-task-status.yml@v0.6.1
    secrets:
      PROJECTS_WRITE_PAT: ${{ secrets.PROJECTS_WRITE_PAT }}
```

workflow は**自身の tag と一致する furrow release バイナリ**を DL する
（checksum 検証つき）— workflow と binary の版がズレることは構造的にない。CI の
更新は pin を上げた時だけ。認証は fine-grained PAT 1 本（`PROJECTS_WRITE_PAT`:
tracker repo の Contents Read & write のみ）。未設定の間は job は green のまま
スキップ（dormant）。検証は非ブロッキング: 不正な id/lane は報告されるが merge は止めない。

---

## 開発

```sh
go build ./...
go test ./...
go run ./cmd/furrow --help
```

決定論は load-bearing な不変条件なので、golden-file の往復テスト（write → read → write がバイト一致）と「マーシャラ経路は 1 本だけ」を確認する CI ガードで守る。シャード（`tasks/<id>.json`）を書く経路を `core.MarshalTask` 以外に増やしてはならない。

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

---

## ステータス

core（一級の `repos`・board layout v3・version gate）・config・store・app・CLI（`repo`・draft・`-r` スコープ・`apply`・`sync` 含む）・TUI（`furrow ui`）・`migrate` が動作する。リリースは `v0.1.0`〜`v0.6.1` が公開済み（GoReleaser → Homebrew tap・task-status Action は `v0.5.0` から同梱）。将来（低優先）: read-only の Web ビューア。

## ライセンス

MIT © akira-toriyama
