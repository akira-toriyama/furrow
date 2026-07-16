# furrow

> English: [README.md](README.md)

**furrow** は **GitHub Projects / Issues の代替**——clone できる git ネイティブなプレーンテキスト・タスクトラッカー。Go 製の単一バイナリで、構造化メタデータを 1 タスク 1 つの JSON シャード `.furrow/tasks/<id>.json` に、長文の散文を `.furrow/bodies/<id>.md` に分けて持つ。ユーザとコーディングエージェントの両方が、git で綺麗に diff できる素のテキストとしてトラッカーを編集できることを最優先に設計している。畝（furrow）を一本ずつ進めるように、レーンを消化していく。

- **module**: `github.com/akira-toriyama/furrow`
- **Go**: 1.25+

---

## なぜ Issues でないのか

Issues への不満は単純である。**issue は clone できない**——プレーンテキストなら clone でき、オフラインで動き、コードと一緒に grep できる。エージェントは API クライアントなしで、普通のファイル操作と CLI だけでトラッカーを**読み書き**できる。そしてトラッカーが作業と同じ git に住むので、**status が実作業から剥離しない**——コードを変える push がタスクも変えられる。書き込みはバイト安定なので、`git diff` には実際に変わったものしか出ない。

**どちらを使うか。** GitHub Issues は *誰からでも受け付ける* 受付口——write 権限のない他人でもバグを投げられる公開インボックス——に向く。furrow はその逆で、あなたとエージェントのための *非公開・内輪* のタスク管理に向く。「タスクを作るには push できる必要がある」のは **欠陥ではなくアクセス制御**である——コードを守るのと同じ権限境界がバックログも守る。

**ローカルで即時、往復なし。** これらの多くは GitHub でも *できる*——ただし API 経由で。オンライン限定・レート制限あり・1 回ごとにネットワーク往復が要る。furrow はディスク上の素のファイルに対してやる——ミリ秒・オフライン・クォータなし。backlink がその具体例だ。`show --backlinks` は「このタスクに言及しているのはどれか」（本文中の `[[id]]` リンク）をローカルのファイル走査で答える。GitHub の同等物はオンラインの "mentioned in" パネルで、API 呼び出しの向こう側にある。

**複数人について正直に。** furrow は今日のところ *1 人運用が第一* で、そこが磨かれた道だ。複数人でも 1 つのボードを回せる——git repo なので clone して push して `furrow sync` するだけ（1 タスク 1 シャードなので同時編集は綺麗な union になる）。ただし個人向けの機能——`@mention` とタスクの **担当者（assignee）**——は **まだ未実装**で、恒久的な非目標ではなくロードマップ上にある。

furrow はこれを Go で実装する。外部サービス連携は持たず、git repo に commit して綺麗に diff できれば十分、という割り切りである。furrow が意図的に「やらないこと」とその理由は [`docs/non-goals.md`](docs/non-goals.md) にまとめてある。

## 使い方は二通り

- **中央ボード** —— clone できる 1 つのトラッカー repo が**全 repo** を背負う。各タスクは関連 repo を一級の `repos` フィールド（`owner/repo`）で持ち、各 checkout は自分の repo に自動スコープされ、複数マシンの clone は `furrow sync` で収束する。これが GitHub Projects 代替のモード——[中央ボード](#中央ボード)を参照。
- **リポローカル** —— もう一つの使い方: 1 つの repo がコードの隣に自前の `.furrow/` を持つ（`furrow init` するだけ）。従来のモードで、今も完全サポート。以降のクイックスタートはこの形で進める（ボードのスコープ以外は中央ボードでも同一に動く）。
- **standalone（ローカル・remote 無し）** —— 1 台のマシン上で自前の git に持ち、push しないボード（`furrow sync` も CI も無し）。共有トラッカー repo を作れない会社マシンでの定番 —— [standalone](#standalone-リモート無しのローカルボード)を参照。

---

## ストレージ（ハイブリッド）

ストアは repo 内の `.furrow/` ディレクトリ一つに収まる。

```
.furrow/
  tasks/
    t-0001.json     # 1 タスク 1 つの構造化メタデータシャード（機械が書く・決定論シリアライズ）
    t-0002.json
  bodies/<id>.md    # 1 タスク 1 つの長文 markdown 本文（人/エージェントが編集可）
  repos/<owner>__<repo>.json  # repo ごとのレビュー shard（furrow review <repo>）
  meta.json         # ボード全体のレイアウト版（{"schema_version": 5}）。上げるのは `furrow upgrade` だけ
  config.toml       # 人が編集する設定（furrow からは READ のみ）
  archive/          # 退避した古い done タスク（独自の tasks/ + meta.json + bodies/）
```

役割分担はこうなっている。

- **`tasks/<id>.json`** = 構造化メタデータだけ、1 タスク 1 ファイル。小さく、`jq` や Go で即クエリでき、フィールド単位で diff できる。**唯一の決定論マーシャラ（`core.MarshalTask`）からしか書かれない。**
- **`bodies/<id>.md`** = 素の markdown。エスケープなし、タスク単位で diff できる。**手でも Claude でも自由に編集してよい。**
- **`meta.json`** = ボード全体のレイアウト版（`{"schema_version": 5}`）だけを持つ専用ファイル。**シャードの中には決して入れない**ので、版を上げても触るのは 1 ファイルだけで、どのシャードも git のマージ点にならない。この版は**書き込みの入力であって出力ではない**——通常の write は決してこの数字を動かさず、上げられるのは `furrow upgrade` だけ（[レイアウト版ゲート](#レイアウト版ゲート書き込みを止めるのは版が違うとき)を参照）。
- **`config.toml`** = 人が編集する設定。furrow は書き換えず、READ するだけ。
- **`archive/`** = 古くなった done タスクの退避先（独自の `tasks/` + `meta.json` + `bodies/` を持つ兄弟シャードストア）。

かつては単一のソート済み JSON 配列だったが、2 人の作業者が別々の worktree／PR でタスクを追加・編集すると git のマージで衝突していた。1 タスク 1 ファイルなら、別々の id は別々のファイルに触れるので、git のマージは衝突なしの union になる（`bodies/<id>.md` は元から per-file で衝突なし——この設計はその利点をメタデータへも広げる）。長文を 1 行のエスケープ文字列に潰す純 JSON 単一ファイルや JSONL を避け、メタと本文を分離するのがこの設計の肝である（詳しい理由は [`docs/non-goals.md`](docs/non-goals.md)）。

### 画像・メディアの添付

タスク本文は素の Markdown なので、スクショや図はファイルを bodies の隣に commit し、**相対パス**でリンクすれば添付できる。

```markdown
![repro](assets/t-0001-bug.png)
```

Markdown が描画される場所（GitHub・Obsidian・エディタのプレビュー）では表示されるが、**端末では表示されない**（`show` は絵ではなくテキストを出す）。furrow 自体はこれらのファイルを特別扱いせず、ただの repo の一部として扱う。実務上の注意：

- スクショは小さく保ち、秘匿情報はマスクする（git 履歴は永久）。
- **private** repo では、画像を repo 内に commit して相対リンクするのが確実（外部/raw の画像 URL は認証が要り失効する）。public repo なら外部ホストへのリンクも可。
- 動画など大きいメディアは、**最初の 1 つを commit する前に** Git LFS（`.gitattributes`）で track する（後から LFS を入れても効くのは新規ファイルだけで、既存 blob の掃除には履歴書き換えが要る）。
- `furrow lint` がこの習慣を補強する：参照先が無い body の asset 参照・どの body からも参照されない asset・5 MiB 以上の asset を warn する——**履歴に blob が載る前に** LFS 追跡か縮小を促す（commit 後は消せない）。

### 決定論（生命線）

各シャードの書き込みは `core.MarshalTask` という**唯一の経路**を通る。契約は以下のとおり。

- key 順 = struct のフィールド宣言順
- 2-space インデント
- `SetEscapeHTML(false)`（CJK や `< > &` がそのまま残る）
- 空のコレクションは `null` でなく `[]`
- label / dep 集合はソート＆重複除去
- タイムスタンプは UTC・秒単位（RFC3339 の `...Z`、ナノ秒なし）
- 末尾改行あり

この結果、**`furrow` が書いたバイト列と、人や Claude が手編集したバイト列が一致する**。しかも Save はバイト列が変わったシャードだけを書くので、no-op の保存では git の churn はゼロになる。`meta.json` はさらに徹底していて、Save は**そもそも書き換えない**（ボードの申告版は write の検査対象＝入力である）。この 1 ファイルに commit が立つのは `furrow init` と `furrow upgrade` のときだけ。

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

配布は GoReleaser から Homebrew tap（`akira-toriyama/homebrew-tap`）と nix flake へ流す（公開済みリリースは [Releases ページ](https://github.com/akira-toriyama/furrow/releases) を参照）。nix flake の `vendorHash` は `v0.4.0` で実 hash 化済み（`flake.lock` も commit 済み）。リリースパイプラインは各成果物に GitHub の build-provenance attestation を付与する（`gh attestation verify <file> --repo akira-toriyama/furrow` で検証）。各 archive には SPDX SBOM（`<archive>.spdx.sbom.json`。release assets と `checksums.txt` に載る）も付き、それ自体に署名付き attestation が付く（`gh attestation verify <archive> --repo akira-toriyama/furrow --predicate-type https://spdx.dev/Document/v2.3` で検証。predicate-type は SPDX 版から導出されるため、リリース側で SPDX 2.3 に固定している）。

---

## クイックスタート

```sh
# repo 内でストアを初期化
furrow init

# タスクを追加（id は自動採番・凍結。bodies/<id>.md も作られる）
furrow add "config.toml ローダを書く" --label core --priority 100

# 一覧（lane->priority->id の正準順）
furrow ls

# いま着手すべきタスクを ready へ（add の既定レーンは inbox）
furrow move t-0001 ready

# いま着手できるタスク（[next].lanes — 既定 ready + in-progress — にあり、依存が全部 done）
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

今日時点で**実装済み**のコマンド（全て動作する）。furrow は **CLI 専用**で、TUI/GUI は CLI/JSON 契約経由で furrow を駆動する別立ての（予定の）フロントエンド。

| コマンド | 説明 |
|---|---|
| `init` | カレントディレクトリに `.furrow` ストアを作る（`config.toml` + `meta.json` + 空の `tasks/` + `bodies/`） |
| `add <title>...` | タスクを追加（`--stdin` で標準入力から1行1タスクを一括作成）。id を自動採番し `bodies/<id>.md` を作る。`--check`（反復可）で checklist 項目を seed（body prose だけでは shard checklist に入らない）。範囲外 `--value`/`--effort` は clamp＋stderr note。`-` 始まり title は `--` 区切りが必要（エラーが案内）。`--type` で work-item 型を設定（`[types].order` の値、例 `epic`。未知型は exit 2＋`candidates`） |
| `ls`（別名 `list`） | タスクを正準順（`lane -> priority -> id`）で一覧。`--drafts` で repo 未付与のタスク（draft）だけを一覧（ボードのスコープは無視）。`--since`/`--until` は `updated` で期間フィルタ（素の `YYYY-MM-DD`、または RFC3339。素の `--until` はその日を丸ごと含む）。`--sort updated\|created\|value\|effort` で並べ替え（新しい/大きい順。`--reverse` で反転、未設定 `value`/`effort` はどちら向きでも末尾）。`--sort` 時は `-n` がソート後の上位 N。未知 `--sort` フィールド・不正な日付は exit 2。`--archived` は hot ボードでなく archive store（`.furrow/archive/`）から一覧（同じフィルタ/sort が効く）。フラットな各行は 1 文字の**状態記号**を持つ（★ 着手可能＝next レーンかつ依存が全部 done＝まさに `furrow next` が渡すもの / ✓ done / ~ 保留（done でない terminal レーン）/ ▣ container の箱 / · 着手不可）。`--json`/`--ndjson` は各行に `actionable`・`blocked_by`・`container`・`stuck` を付ける（以前は `--tree` だけが持っていた派生事実）。状態で絞るには **`--actionable`**（★ だけ）か **`--blocked`**（依存が満たされていない行だけ）—— どちらも `-s/-l/-r` と AND（`-s ready --blocked` = ready なのに実は詰まっている行）で、両者は排他（同時には成立しない）。**`--tree`** は同じ事実を、フラットな表でなく **parent の階層**として描く —— top-level タスクごとに 1 本の木、`<id>` を渡せばその部分木（`furrow ls --tree <id>`）。フィルタはそのまま効き、森は**マッチした集合の上に**組む —— 親がフィルタで落ちた子は「消える」のではなく root になる（`--tree` を付けたせいで見えるタスクが減ることは無い）。`--tree` 時の `-n` は**木の本数**の上限（タスク数ではない＝途中で切ると見せた木から子を切断してしまう）。`--tree` では `--json` が children を入れ子にし（各ノードに同じ `actionable`・`blocked_by`）、`--ndjson` は 1 行 1 本の木。**container**（epic）のノードは子の進捗ロールアップ（`progress` = `done/total`、既定は直下の子・`--progress-recursive` で部分木全体）と `stuck`（配下に open な仕事があるが actionable な子孫が無い）も持つ。`--type` で型フィルタ（effective 型で照合＝`--type task` は型無しの多数派も含む） |
| `show <id>...` | タスク（複数可）を markdown 本文付きで 1 回の読みで表示（入力順。複数 id は `--json` で配列／human は `---` 区切り、1 id は従来どおり単一オブジェクト。`--ndjson` は個数によらず 1 行 1 タスク）。`--no-body` で本文（`body_text`）を省く＝agent 向けの軽量メタデータ読み。一部 id が見つからなくても見つかった分は出力し、exit 1 のエラーに `details.missing` が載る。見つからない id が実は **archive 済み**なら、`details.archived` に載せ、メッセージが `--archived` での再試行を促す。`--archived` は archive store（`.furrow/archive/`）から読むので、退避済みタスク（と `[[id]]`/`SetStatus-task` リンク）が引き続き辿れる。`--backlinks` は本文でこのタスクを `[[id]]` で参照する他タスクを列挙（「Mentioned in」節／`--json` では `mentioned_by` 配列。GitHub の "mentioned in" のローカル・レート制限なし版）。`--archived` とは併用不可 |
| `next` | 着手可能なタスク（設定 `[next].lanes` — 既定 `ready` + `in-progress`、intake レーンは出ない — にあり、依存が全部 done）を表示。**container** 型（epic）は箱であって仕事ではないので出さない —— `--containers` で着手可能な箱も出す。**`--lanes <csv>`** はこの呼び出しだけ「今」とみなすレーンを上書き（config の `[next].lanes` は書き換えない＝非破壊）—— `next --lanes backlog,ready` は昇格前でも今すぐやれる無依存の backlog タスクを出す。依存が全部 done の条件は不変で、未知のレーンは exit 2＋`candidates`（`-s` と同様）。`--json`/`--ndjson` は各タスクに `reason`（`in_next_lane`＝マッチしたレーン名なので `--lanes` で拾ったものと区別できる・`deps_satisfied`）を付与 |
| `revisit` | read-only。再評価すべき open タスクを一覧。`--json`/`--ndjson` は各タスクに `revisit` 配列 `{code, detail}`（`no_repo`・`value_unset`・`effort_unset`・`stale`・`dep_done`）を付与し、エージェントが何を直すか分かる。draft はスコープに関係なく浮上する。空でも exit 0。`-l/--label`・`-r/--repo`・`-n/--limit`・`--stale-days <n>`（0 で stale 無効） |
| `search <term>` | タスクの**タイトルと markdown 本文**を全文検索（大文字小文字を無視した部分一致）、canonical 順。`.furrow/bodies` を `grep` する寄り道でなく 1 コマンドで探せる。`ls` と同じ `-s/-l/-r/-n` スコープを尊重（素の `search` はこの repo のボード内。`-r ''` で全ボード）。各ヒットは `matched_field`（`title`\|`body`）と、語を文脈付きで示す 1 行 `snippet` を返す。タイトル一致なら本文は読まない。複数語は 1 つのリテラル句。空でも exit 0。`-s/--status`・`-l/--label`・`-r/--repo`・`-n/--limit` |
| `stats` | スコープ内でボードを集計: `total`・`drafts`、および `by_lane`（設定レーン順の完全ヒストグラム。件数 0 のレーンも含む）・`by_repo`・`by_label`（使用語彙＝多い順）。素の `stats` はこの repo の断面、`stats -r ''` は全ボード —— `-l`/`-r` を推測する前に label/repo 語彙を知る呼び出し。`--json`/`--ndjson` は 1 オブジェクト。全 0 のボードでも exit 0。`-s/--status`・`-l/--label`・`-r/--repo` |
| `board` | アクティブなボードの introspection スナップショットを出す: store パス・discovery `source`（`env`/`local`/`pointer`/`user-config`）・repo スコープ・レーン語彙（`lanes`/`next_lanes`/`default_lane`/`done_lane`/`terminal`）＋ stale/archive の窓＋**スキーマ三つ組**（`schema_version`＝ボードの申告値（0 = 不在または読めない）・`binary_schema_version`・`schema_state`＝`current`/`outdated`/`too-new`/`unreadable`・`writable`）。**エラーを起こさずレーンを知る**手段であり、**版の食い違いでも失敗せず報告する**——他のどのコマンドも開けないボードを診断できる唯一の pre-flight。`--json`（or `--ndjson`）でオブジェクトを出力 |
| `edit <id>` | `bodies/<id>.md` を `$EDITOR` で開く（非対話ならパスを出力） |
| `note <id> <text>` | `<text>` を body に新しい段落として追記し、**同時に** タスクの `updated` を進める（1コマンドで）。セッションを跨ぐ経過・停止点・次の一手を残す in-band な手段。`edit` でファイルを直接編集した場合と違い `updated` が正しく進むので、body だけで reconcile 済みのタスクに `lint` の `reconcile-gap` が誤発火しない。`<text>` に `-` を渡すと stdin から読む（複数行・長文用）。`--json` は `{before,after,changed}` に加えて `appended`（追記テキスト）を出す（body だけ動いたときメタの `changed` は `[]`） |
| `attach <id> <file>` | 画像/動画を `bodies/assets/<id>-*` にコピーし、body に相対 markdown 参照を追記する。画像は埋め込み（`![…]`）・その他媒体はリンク（`[…]`）。衝突しない名前（`…-2`, `…-3`）で既存アセットを上書きしない。body は commit される markdown なので、web アップロード無しに端末だけで attach 全体が git に載る。LFS 非依存。`--json` は `{id, asset, ref, line}` を出力 |
| `done <id>` | done レーンへ移動し `closed` を打刻 |
| `move <id> <lane>` | 任意のレーンへ移動 |
| `reorder <id> <priority>` | priority（疎な整数）を設定 |
| `retitle <id> <title...>` | タイトルを変更。シャードの title **と** body 先頭の `# ` 見出しを両方更新して食い違わせない（末尾の引数は空白で連結するのでクォート不要） |
| `value <id> <1-5>` | 粗い value（重要度）見積もりを設定。範囲外は 1..5 に丸め、**さらに signal**（`--json` の mutation 封筒に `clamped {requested, stored}` キー＋stderr note＝明示引数を黙って丸めない）。`--clear` で未設定に戻す |
| `effort <id> <1-5>` | 粗い effort（手間）見積もりを設定。`value` 同様 1..5 に丸め＋`clamped` signal。`--clear` で未設定に戻す |
| `set <id>` | routine triage（lane・value・effort・label・type）を **1 回の write** でまとめて適用（`move`+`value`+`effort`+`label` を 1 つに）。最低 1 変更が必要。未知レーン/型は `move` 同様 exit 2＋`candidates`。`[labels].required` 下で最後のラベルを剥がす set は拒否 |
| `check <id> [index]` | チェックリストを編集: 0 始まり index の項目を done にする（トグルでなく冪等 set。`--off` で外す）・`--add` で追加（反復可・verbatim）・`--rm` で index の項目を削除・`--reword <text>` で index の項目テキストを差し替え。mode フラグは排他、範囲外 index は exit 2 |
| `dep <id> [<dep-id>...]` | 依存を 1 つ以上まとめて追加（id がそれらを待つ）。`--rm` で削除。循環防止・冪等・all-or-nothing（不正 dep-id は部分適用せず abort）。`--list` は mutate せず `<id>` の依存近傍を**両方向**で読む —— `depends_on`（待っている先＝自分の deps）と `blocks`（逆辺＝このタスクを待っている側。「これを終わらせたら何が解ける？」ビュー）を id+title+lane に解決。`--json`/`--ndjson` は両配列を持つ 1 オブジェクトを出力（空は `[]`）。dangling dep は id だけに解決（lint が指摘）。`--list` は id のみで `--rm` とは併用不可 |
| `parent <id> [<parent-id>]` | `<id>` を `<parent-id>` の下に置く（階層）。`--rm` で親を外して top-level に戻す。これまで `parent` は `add --parent` の書き切りで、間違えたら**機械が書く shard を手編集**するしかなかった（CLAUDE.md が禁じている行為）。循環防止: 親は存在必須・自分自身は不可・ループを閉じる辺は exit 2（**循環した階層は root を持たない**＝その中の全タスクがどの木にも属さなくなる）。**done な親は許可**する —— 終わった epic の下に取りこぼしを戻すのは正当な記録だから。開いたままの子は `lint` の `parent-done`（warn）が知らせる。`--list` は mutate せず階層近傍を**両方向**で読む —— ぶら下がっている `parent`（top-level なら `null`）と、下にいる `children`（無ければ `[]`）を id+title+lane に解決 | `--rm`, `--list` |
| `label <id>` | ラベルを追加／削除（`--add`・`--remove`、いずれも反復可・併用可）。冪等 |
| `repo <id>` | repo（`owner/repo`）を追加／削除（`--add`・`--rm`、反復可・併用可）。値は完全な `owner/repo` か、ボード既知の repo に一意に解決する短名のみ（それ以外は exit 2・`candidates` 付き）。冪等。repos が空のタスクは draft |
| `review <repo\|id>` | レビューを記録（非対話）。id 形の引数はそのタスクの `reviewed` タイムスタンプを打つ（`updated` とは別管理＝レビューは内容を変えない）。それ以外（完全な `owner/repo` か一意な短名）は repo 単位のレビュー時計を記録。`--by human`（既定）は staleness nudge の時計（`last_reviewed`）を進め、`--by agent` は sweep（`last_agent_reviewed`）を記録するが人間の時計は進めない（自律再評価が人間への nudge を止めない） |
| `apply` | PR/コミット本文から `SetStatus-task: <body-link> [<lane>]` ディレクティブを解析して適用（stdin または `--body-file`）。status 自動更新の CI フック。`--on open` は in-progress へ寄せ、`--on merge` は lane を適用。検証は非ブロッキング |
| `sync` | マルチマシン運用の儀式を 1 コマンドで: `.furrow/` 限定の auto-commit（機械が書く shard は常に commit、手編集の `bodies/<id>.md` は新規か `-b` 明示時だけ・それ以外は `pending_bodies` に残して作者に委ね、共有 checkout が他人の WIP を巻き込まない。`--all-bodies` で従来の全 sweep）→ `fetch` + `rebase --autostash @{u}`（`FETCH_HEAD` でなく追跡 ref に rebase、他 writer の fetch と race しない）→ `push`（non-fast-forward 時は pull→push を 1 回リトライ）。conflict 時は自動 abort（`sync-conflict` エラーにパス一覧）。pre-flight が捕まえた他人の rebase は待って吸収、超過時は retryable `sync-busy`（exit 3）。pull 中の fetch/ロック競合はリトライし、解消しなければ（stale な `.git/*.lock` の可能性）除去すべきロックを名指して terminal に失敗。自分の dirty ファイルは rebase のため autostash されるが、git がそれを**戻せなかった場合は stash に置いたまま exit 0 する**ので、sync は stash を自分で確認し `sync-stash-stranded`（exit 3・push しない）で失敗する。pop されるまで `pending_stash` に出続ける。conflict marker を含む body は auto-commit しない（`body-conflict-marker`、exit 2）。進捗 `{committed, pulled, pushed, conflict, committed_bodies, pending_bodies, pending_stash}` は失敗時も stdout に出る。成功時は repo スコープの `revisit` サマリ（`dep_done`/`stale` の id 一覧。空なら省略）も付く |
| `archive [<id>...]` | done タスクを `.furrow/archive/` へ退避（`--yes` なしはプレビュー）。`<id>` 指定でそれらを名指し退避（各々 done レーン必須・違えば exit 2＝進行中を stranding しない）／id 無しは古い done を sweep。sweep は既定で全 repo 対象、`-r/--repo`（繰り返し可）で 1 repo に絞る（age ガードと AND）。`--older-than`/`-r` は sweep 専用（id 列との併用は exit 2）。タスクの `attach` した媒体（`bodies/assets/<id>-*`）はタスクと一緒に `.furrow/archive/` へ移動し、hot store に取り残されない |
| `upgrade` | ボードの on-disk レイアウト版（`.furrow/meta.json`。archive ストアがあれば `archive/meta.json` も）をこのバイナリが書く版へ引き上げ、全 shard を現行マーシャラで再シリアライズして 1 回の意図的な commit にまとめる。**ボードの版を動かす唯一の手段**——通常の write は代わりに拒否する（`schema-upgrade-required`・exit 2）ので、レイアウト移行が `sync` の副作用で起きることは二度とない。`--yes` が無ければ preview（`archive` と同じ破壊操作ガード）で、flag-day チェックリスト（furrow を release → 全 caller の pin を上げる → その後に upgrade）を印字する。既に現行版なら綺麗な no-op（`changed:false`・exit 0・書き込み 0 バイト）。ボードの方が新しければ拒否（`schema-too-new`・exit 3）——**降格は無い**ので、戻すならボード repo を `git revert` する。`--json`/`--ndjson` は `{from, to, changed, applied, stores:[{path, from, to, tasks}]}` |
| `lint` | shard↔body の整合・レーン・依存・config を検査（依存の循環（`dep-cycle`）と**階層の循環**（`parent-cycle`）はどちらも error＝循環した階層は root を持たず、その中の全タスクがどの木にも属さなくなる。done な親の下に開いたままの子が残っていれば `parent-done`（warn）＝epic が仕事を残して閉じた状態（親を付け替えるか、親を開き直す）。closed 無しの done レーンタスクも error＝`furrow done` で backfill、body に残った git の conflict marker は `conflict-marker`＝**error**（半分マージされた body は「進捗の正本」が半分欠けた状態。`furrow sync` は commit を拒否するので、これは既にボードに載ってしまった分の検出器。``` フェンス内の marker は「解説」なので対象外）、存在しない id への `[[id]]` リンクは warn＝archive 済み id は dangling 扱いしない。done な依存が最終更新後に閉じた open タスク＝reconcile gap も warn。アセット衛生＝参照先が存在しない body の asset 参照・どの body からも参照されない orphan asset・5 MiB 以上の oversized asset はいずれも warn（生 blob は commit 後に消せないので着地前に検出。Git LFS 追跡か縮小を促す）。バイナリより古いレイアウトのボードは `schema-outdated` を warn（error にしない＝read-only なボードは flag day の正当な途中経過で、全 repo の CI を赤くする話ではない）。furrow が知らないキーを持つファイルは `unknown-shard-key` を warn（機械が書く 3 種すべて＝task shard・`repos/` review shard・`meta.json` を検査。[保持](#未知のキーは捨てずに保持する)はされるが**無視**されており、スキーマ側が未知キーを許すようになった今これが唯一の検出器＝furrow を更新するか、手編集のタイポを直すか）。書きかけのユーザー設定の clamp 警告も含む。`[lint].archive_done` 設定時は、archive 可能な done がその件数に達すると `archive-backlog` nudge も出す。各 finding は安定した kebab-case の `code`（`dangling-link`・`dep-cycle`・`orphan-asset`・`archive-backlog`・`schema-outdated`・`unknown-shard-key` …）を持ち、`--json`/`--ndjson` の triage は message 文でなく code で分岐できる＝`id` は文脈依存（task id・asset 名・`owner/repo`・`meta`・`config`）。出力は `--code`（許可リスト）/ `--exclude-code`（除外リスト＝`--code` に勝つ）/ `--severity error\|warn`（厳密一致）で絞れる —— 未知の `--code`/`--exclude-code` トークンは exit 2＋`candidates`（レーン同様の閉じた語彙）、一方 config の `[lint].ignore_codes` は毎回のコードを抑制し、未知エントリは *warn* するだけ（clamp-don't-reject）。**フィルタが exit code を決める**: 絞り落とした problem は lint が見つけなかったのと同じ扱いになるので、最後の error を除外／ignore すれば exit 0（狙い＝`reconcile-gap` のような恒久的に死んだ検査を黙らせて CI を赤くしなくする）、`--severity warn` は常に exit 0（error があってもフィルタで隠れる）） |
| `config init` | ユーザー設定 `~/.config/furrow/config.toml`（中央ボード雛形）を書き出す。ボード内で実行すると最寄りの `.furrow` から path/scopes を文脈導出、離れていればコメント付き placeholder。既存ファイルは上書きしない（`--path`・`--scope`（複数可）） |
| `config path` | 解決されるユーザー設定パスを表示。書きかけ設定の clamp 警告は stderr へ（stdout は path のみ） |
| `schema [task\|meta]` | JSON Schema を出力（引数なし or `task` = シャード（`tasks/<id>.json`）のスキーマ・`meta` = `meta.json` のスキーマ） |
| `version` | furrow のバージョンを出力（stamp 済みならビルド commit/date も）。root の `--version` フラグでも同じ行を出力。`--json` は `{version, commit, date, modified}` を出力（スクリプト／エージェント向け） |
| `migrate <file>` | 既存の `Task.md` などを取り込む（dry-run 既定／`--write` で作成・未対応の見出しや `[[wikilink]]` は破棄せず報告） |

### 主なフラグ

- `--status, -s <lane>` — レーンで絞り込み（`ls`）。1 つの値の中でカンマは OR（`-s inbox,backlog`。トリムし空要素は無視）。**`-s` は繰り返しても union**（`-s inbox -s backlog` == `-s inbox,backlog`）＝繰り返しが黙って最後の1つだけになる（last-wins）ことはもう無い。レーンは閉じた語彙なので**未知レーンは exit 2**（設定済みレーンを `candidates` に載せる。`move`/`add` と対称＝`-s in_progress` の打ち間違いが `[]` に化けない）。一方 `-l`（ラベル）は開いた語彙で未知タグは無マッチのまま。レーン一覧は `furrow board` でエラーを起こさず確認できる
- `--label, -l <label>` — ラベル（純粋なタグ）で絞り込み（`ls`/`next`/`revisit`。スコープと AND、値内はカンマで OR＝`-l bug,urgent`。**読み取りでは繰り返しも union**＝`-l bug -l urgent` == `-l bug,urgent`。黙って last-wins にならない）。`add` では `-l` 繰り返しでラベルを付与
- `--repo, -r <owner/repo|短名>` — repos フィールドで絞り込み（`ls`/`next`/`revisit`）。短名は `/` 境界で大文字小文字を無視して解決（`-r furrow` → `akira-toriyama/furrow`。曖昧なら exit 2・`candidates` 付き）。明示 `-r` はボードのスコープを上書きし、`-r ''` で全件。`add` では `-r` 繰り返しで repo を付与
- `--drafts`（`ls`）/ `--draft`（`add`） — draft（repo 未付与タスク）だけを一覧／draft として作成（`--draft` は `-r` と併用不可）
- `--limit, -n <N>` — 行数の上限（`ls` / `next`。`0` は全件、`next` は `-n1` で先頭だけ）
- `--priority, -p <N>` — `add` で priority を明示（省略時はレーン末尾に追記）
- `--value <1-5>` / `--effort <1-5>` — `add` で粗い value/effort 見積もりを付与（範囲外は 1..5 に丸め・省略で未設定）
- `--parent <id>` / `--dep <id>`（繰り返し）/ `--ref <file:line|URL>`（繰り返し）/ `--body <md>` — `add` のメタ指定
- `--add <text>`（反復可）/ `--off` — `check` のチェックリスト操作
- `--older-than <days>` / `-r/--repo <repo>`（繰り返し）/ `--yes` — `archive`（`--yes` なしは dry-run プレビュー。`-r` で対象 repo を絞ると共有ボードで 1 repo の done だけ畳める）
- `--on open\|merge` / `--ref <src>` / `--body-file <path>` / `--open-lane <lane>` — `apply`（`--on` 必須。`--ref` は本文に記録する出典 例 `furrow#42`）
- `-m/--message <msg>` / `-b/--body <id>`（繰り返し）/ `--all-bodies` — `sync`。`-m` は auto-commit メッセージ上書き（既定 `:card_file_box: chore(board): sync via furrow`）、`-b` は手編集した `bodies/<id>.md` を明示 commit（共有ボードで既定 skip される変更済み body を push）、`--all-bodies` は dirty な body を全 commit（自分専有の checkout 向け）

`add` の `--status` 省略時は `config.toml` の `[lanes].default`、`archive` の `--older-than` 省略時は `[archive].older_than_days` が使われる。

---

## CLI 契約（Claude Code / スクリプト向け）

furrow は **非対話がデフォルト**。プロンプトは出さない（TTY 検出は `golang.org/x/term`）。furrow は **CLI 専用**で、対話 UI（TUI/GUI）は CLI/JSON 契約経由で駆動する別立ての（予定の）フロントエンド。

- **`--json`** — JSON を **stdout のみ**に出す。ログ・エラーは stderr へ。read だけでなく **JSON を出す全コマンド**で効く（mutation の `{before,after,changed}`・`apply` の `{on,ref,outcomes}`・`add`/`attach`/`init`/`lint`/`archive`/`migrate`/`version`/`board` も含む）。
- **`--ndjson`** — `--json` と同じ payload を **compact に 1 行 1 値**で出す。`--json` が効く全コマンドで honor（list 系は 1 行 1 レコード、単一オブジェクト系＝mutation・`board` 等は compact 1 行、`lint` は 1 problem 1 行）。line 志向の agent が human prose に silent degrade しない。
- **id 集合の一括読み** — `show <id>... --no-body` で任意の id 集合を 1 プロセス・本文なしで横断取得（監査・依存チェック向け。`--ndjson` 併用で shape が個数非依存に）。
- **フィルタ** — `--status/-s`・`--label/-l`・`--repo/-r`・`--limit/-n`（`-s`/`-l` は値内のカンマが field 内 OR）。未知トークンの扱いは非対称: `-s` の未知レーンは exit 2＋`candidates`（閉じた語彙・`move`/`add` と対称）、`-l` の未知タグは無マッチ（開いた語彙）。明示 `-l X` が 0 件で、X がタスクを持つ repo 短名に一意解決するときは exit 2 で `-r X` へ誘導する（did-you-mean ガード）。レーンとスコープは `furrow board` でエラーを起こさず一覧できる。明示 `-r` が draft を隠したときは stderr に `N draft(s) hidden — furrow ls --drafts` を 1 行出す。
- **破壊操作ガード** — `archive` は `--yes` がない限りプレビュー（dry-run）に留まる。
- **exit code 契約**:

  | code | 意味 |
  |---|---|
  | `0` | 成功 —— **空のクエリ結果も含む**（`ls`/`next`/`revisit` が何にもマッチしなくても成功。`set -e` が「仕事なし」で止まらない） |
  | `1` | **名指しした id** が見つからない（`show <id>` 等）。空リストではない |
  | `2` | bad-usage / バリデーション失敗（引数を直す。リトライしない） |
  | `3+` | 内部 / IO 障害 |
  | `130` / `143` | `SIGINT` / `SIGTERM` で実行が中断された（Unix 慣習の 128+signal）—— 例: `furrow sync` 中の Ctrl-C。`sync-interrupted`（retryable）を返す。意図的な `sync-conflict` は中断ではないので exit `3` のまま |

  **スキーマゲート**だけは「id ではなく exit code が、どちら側が古いかを告げる」唯一の場所なので、明示しておく:

  | id | code | 古いのはどちら | どうするか |
  |---|---|---|---|
  | `schema-upgrade-required` | `2` | **board** —— この binary より古い。読めるが **read-only** | `furrow upgrade`（flag day: pin している呼び出し側を**先に**全部上げる） |
  | `schema-too-new` | `3` | **binary** —— board が知らないレイアウトを宣言している | furrow を更新する。CI なら `sync-task-status.yml@vX.Y.Z` の pin を上げる |

  どちらも `details {board_schema, binary_schema}` を持つ。`schema-too-new` は**意図的な拒否なのに exit 3** である点に注意 —— 直すべきは入力ではなく binary だから。「ここに書けるか？」を**エラーを起こさずに**問うなら `furrow board --json` の `writable` / `schema_state` を読む（版ズレでも失敗しない）。`furrow lint` は `schema-outdated` を warn する。

  この契約は binary の `--help`（root の long help・各コマンドの help）にも載る（ここだけでない）。

- **エラー出力** — 非ゼロ時は stderr に次の形を出す（stdout を `jq` に流していても汚染しない）。

  ```json
  {"error":{"code":2,"id":"t-0042","message":"unknown lane \"foo\" (configured: inbox, backlog, ready, in-progress, waiting, done, icebox)"}}
  ```

  入力が「あと一歩で解決できた」とき（repo 短名の曖昧・未知レーン・親コマンドの未知サブコマンド `config show`・ラベルが repo を一意に指す did-you-mean ガード）は、封筒に `"candidates": [ … ]` も載る。スクリプトはメッセージ文をパースせず、この配列から選べばよい。同様に `show` の一括読みで一部 id が見つからないときは、見つかった分を stdout に出した上で exit 1 になり、封筒に `"details": {"missing": ["t-…", …]}` が載る — 判定は配列で、メッセージ文では行わない。

- **レイアウト版ゲート** — 書く前に `furrow board --json` を pre-flight に読む（版が食い違っても失敗せず報告する唯一のコマンド）。`writable` / `schema_state`（`current`/`outdated`/`too-new`/`unreadable`）で分岐すればよく、失敗した write で気付く必要はない。ボードがバイナリより古ければ write は `schema-upgrade-required`（exit 2＝`furrow upgrade` を回す）、新しければ `schema-too-new`（exit 3＝furrow を更新する）。どちらの封筒にも `"details": {"board_schema": N, "binary_schema": M}` が載る。詳しくは[レイアウト版ゲート](#レイアウト版ゲート書き込みを止めるのは版が違うとき)。

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
git repo の外でも同様（ボードは開く・注記のみ）。`FURROW_BOARD=<path>` は中央ボードの env 版で、
user-level の config ファイルの `[[board]]` 群を単発・テスト用の単一ボードで置き換える（scope は
board の repo 親）。ただし**より近いストアは上書きしない**——`FURROW_DIR`・ローカル `.furrow`・
`.furrow-pointer.toml` はいずれも `FURROW_BOARD` に勝つ（発見の優先順位を参照）。廃止された
`label = "auto"` は警告付きで無視され、`repo = "auto"` へ誘導される。

#### per-repo pointer

特定の 1 repo は、直下の `.furrow-pointer.toml` で redirect できる（user-level の中央
ボードに**優先**する）:

```toml
board = "../projects/.furrow"   # 中央 .furrow（本ファイル基準の相対・~・絶対）
default_repo = "me/chord"       # 任意: 1 owner/repo にスコープ（"auto" で導出 / "" = redirect のみ）
```

#### 発見の優先順位

`FURROW_DIR`（明示・スコープ注入なし）→ 直近の親で `.furrow` を持つディレクトリ（実体の
ローカルストアが勝つ）→ `.furrow-pointer.toml`（ボードへ redirect）→ **中央ボード**：
`FURROW_BOARD`（env 上書き・単一 synthetic ボード）があればそれ、無ければ user-level config
ファイルの `[[board]]` 群（cwd が `scopes` のいずれか配下のとき・最長一致が勝つ）→ `furrow init`。
つまり `FURROW_BOARD` は config ファイルのボードにのみ勝ち、より近い `FURROW_DIR` / ローカル
`.furrow` / pointer には決して勝たない。

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

1. `.furrow/` **限定**の auto-commit（board repo にある他の dirty ファイル＝ノート類は
   巻き込まない）。`.furrow/` の中でも、機械が書く shard（`tasks/`・`meta.json`）は常に
   commit するが、手編集の `bodies/<id>.md` は**新規のとき、または `-b/--body` で明示した
   ときだけ** commit する —— 単に変更されただけの body は作者に委ね（`pending_bodies` で
   通知）、共有 checkout が同居する他オペレータの WIP を誤った author で commit しないように
   する。`--all-bodies` で従来の全 sweep に戻せる（自分専有の checkout 向け）。既定メッセージ
   `:card_file_box: chore(board): sync via furrow`、`-m` で上書き
2. `git fetch` → `git rebase --autostash @{u}` —— `FETCH_HEAD` ではなく上流の
   **追跡 ref** に rebase するので、共有 checkout で他 writer の並行 fetch が
   `fatal: Cannot rebase onto multiple branches` を起こせない
3. `git push`（non-fast-forward なら pull→push を 1 回リトライ）

shard 化により本当の conflict は稀（別マシンの add 同士は別ファイル）。**同じ** task を
両側で編集したときだけ conflict し、その場合 sync は **rebase を自動 abort** する
（board に conflict marker を残さない・ローカルの sync commit は残る）。exit 3 の error
封筒に `"id": "sync-conflict"` と `"details": {"paths": [...]}` が入るので、agent は
どの shard を手で直せばよいか機械的に分かる。進捗オブジェクト
`{committed, pulled, pushed, conflict, committed_bodies, pending_bodies, pending_stash}` は
成功・失敗を問わず stdout に出る（各リストは空なら省略）。

##### git が返してくれない autostash

手順 2 は、sync commit に含めない**他の dirty ファイル**を autostash して rebase し、
終わったら戻す。この**戻し（再適用）が pull した内容と衝突すると、git は黙る**——
変更を **stash に置いたまま**にし、警告を stderr に出すだけで **exit 0** する。rebase は
「成功」し、あなたの編集は作業ツリーから消えている。exit code には何も現れない。消えたのが
書きかけの `bodies/<id>.md` なら、それは furrow の**進捗の正本**が宙に浮いたということだ。

そこで sync は stash 自体を見に行き、見つけたものを必ず報告する:

- **取り残した sync は失敗する** —— `"id": "sync-stash-stranded"`（exit 3）、
  `"details": {"pending_stash": [{"ref", "commit", "paths"}]}`、push はしない。
  `git stash pop` で回収してから再実行する。
- 残っている autostash エントリは、pop / drop されるまで**毎回の sync が報告する**
  （`pending_stash` と stderr の警告）。誰にも知らされない取り残しこそが、この修正の対象。
  自分で作った `git stash`（subject が `WIP on …`）には一切触れず、報告もしない。
- この失敗が残す index（unmerged だが operation は進行中でない）は、git の不透明な
  `notes.md: unmerged (…)` をそのまま流さず、pre-flight が `"id": "sync-unmerged"`
  （exit 2）で**状態を説明する** —— unmerged なパスと、残り半分を抱えたままの stash を
  両方名指しする。

そして、この再適用失敗が残す**残骸** —— 作業ツリーのファイルに書き込まれた conflict marker
—— は入口で止める: `<<<<<<<` / `=======` / `>>>>>>>` を含む body は **auto-commit しない**
（`"id": "body-conflict-marker"`、exit 2、commit は一切しない）。commit は取り消せないからだ。
すでにボードに載ってしまったものは `furrow lint` の `conflict-marker`（error）が拾う。

**成功**した sync では repo スコープの `revisit` サマリも出る —— オープンな
（未完了の）タスクのうち、既に done な依存を持つもの（`dep_done`）や stale に
なったもの（`stale`）の合図で、詳細は
`furrow revisit` へ。human 出力には 1 行 `revisit: <n> dep_done, <n> stale
(<scope>) — furrow revisit` が追加され（`<scope>` は現在の repo の短名、auto
repo が無ければ `board`）、`--json`/`--ndjson` には id 一覧付きの `revisit` キー
（`{dep_done:[ids], stale:[ids]}`）が乗る。どちらもボードがクリーンなら丸ごと
省略される。

bot や別オペレータが常に push しうるので、共有 checkout は 2 通りに race し、sync は
原因ごとに扱いを変える。(1) pre-flight が**他人の** rebase の一瞬に当たる場合 ——
bounded backoff（〜5s）で**待って吸収**し、まだ続いていれば exit 3 の
`"id": "sync-busy"` を返す。これは「引数を直せ・retry するな」の `exit 2` ではなく
**retryable クラス**で、再実行で解消することが多い（相手が終わっている／本当に stuck なら手で
`git rebase --abort`）ことを示す。(2) **他人の** `git fetch` が ref/index ロックを
こちらの実行中に一瞬奪う場合 —— pull を同じ backoff でリトライする。live な race は
1 秒未満で解消するので、予算超過後もロックが残るならほぼ **stale** ロック（crash した
git が `.git/*.lock` を残した）で、その場合は `sync-busy` で無限ループさせず、除去すべき
ロックを名指して **terminal** に失敗する。

#### ボード用 git hooks（任意）

設計レンズは **remote の自動化 = GitHub Actions、local の自動化 = git hooks**。furrow は
[`scripts/board-hooks/`](scripts/board-hooks/) に POSIX sh の hook を 3 本同梱し、git の拡張点に
`furrow lint` を差し込む。これでボードが不整合になった瞬間（誰かが archive した先に dep が
張られている・孤立 body・merge 由来の重複 shard）に検知でき、**壊れたボードを remote に出さない**。

| hook | 発火 | 動作 | blocking |
|---|---|---|---|
| `post-merge`   | `git merge` / rebase なし `git pull` | `furrow lint` | しない（nudge） |
| `post-rewrite` | `git rebase` / `--amend` / `git pull --rebase` | `furrow lint` | しない（nudge） |
| `pre-push`     | push の前 | `furrow lint` | **error のとき中止** |

blocking するのは `pre-push` だけ、しかも lint の **error 時のみ**（`furrow lint` は error で
exit 2）。warning は素通りし、merge/rebase 後に非ブロッキングで surface する。`git pull --rebase`
は（`post-merge` ではなく）`post-rewrite` を発火するので board は両方入れる。さらに `furrow sync`
は内部で `--rebase` pull するのでこれらの hook を巻き込む —— だから sync 自身は lint を持たない
（hooks に委譲）。

有効化は**マシンごとに 1 回**（git は clone しただけでは hook を有効化しない＝セキュリティ境界。
PC A/B それぞれで要る）。furrow リポ自身と同じ 1 行:

```sh
git config core.hooksPath scripts/hooks   # hook を置いたあと
```

`core.hooksPath` は `.git/hooks` を**上書き**する（足すのではなく、git はこのディレクトリ**だけ**を
見る）。だから既定の `.git/hooks/` に元々ある hook は、この hooks ディレクトリへ**移さないと**黙って
動かなくなる。両方がここに揃ってはじめて、同名 hook（例: `main` を守る `pre-push`）は置き換えでなく
**合成**する対象になる —— 既存本体を残して furrow-lint ブロックを足す。各 hook は `furrow` が PATH に
無い／リポに `.furrow/` が無いときは**綺麗に skip** し、checkout を人質にしない。

---

## 設計の不変条件

- **id は凍結**。`t-k3m9p` 形式（prefix + ランダムな Crockford base32 サフィックス、`[ids].width` 文字）。共有カウンタを持たずローカル生成するので、並行 `furrow add` でも衝突しない。**再利用も再採番もしない**。旧来の連番 id（`t-0042`）も有効で共存。`bodies/<id>.md` のファイル名語幹と 1:1 に対応する。
- **priority は疎な整数**（10 刻みが既定）。並べ替えは `reorder` で 1 フィールドを書き換えるだけ。手リナンバリングは消える。
- **status は `config.toml` のレーン**。Open→Done は値の変更（1 文字 diff）。
- **`done` への移動は `closed` を打刻**。done から外へ移動すると `closed` をクリアする。icebox（温存）のような他の terminal レーンは `closed` を打刻しない（parked と closed は別物）。
- **`next` の定義** = レーンが `[next].lanes`（既定 `ready` + `in-progress` — inbox/backlog などの intake レーンは対象外）にあり、かつ依存（`deps`）が全て done レーンにあるタスク。
- **shard ↔ body は 1:1**。`furrow lint` が、本文ファイルのないタスクと、タスクのない孤立本文の双方を報告する。
- **ボードのレイアウト版は write の入力**。バイナリは、自分と同じ版を申告しているボードにしか書かない（古いボードは読めるが read-only、新しいボードは read も拒否）。版を上げるのは `furrow upgrade` だけで、通常のコマンドの副作用では決して動かない（下の[レイアウト版ゲート](#レイアウト版ゲート書き込みを止めるのは版が違うとき)）。

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
{ "schema_version": 5 }
```

正準スキーマは `furrow schema [task|meta|repo]` が出力する（draft 2020-12）。これが正本で、`docs/schema/furrow.task.v2.json`・`furrow.meta.v2.json`・`furrow.repo.v1.json` が commit 済みのコピー。CI が三者を diff して drift を防ぐ（`v2`/`v1` はスキーマ**文書**の版号で、ボードのレイアウト版＝`meta.json` の `schema_version` は 5）。

### レイアウト版ゲート（書き込みを止めるのは版が違うとき）

`meta.json` の数字は **ボードのもの**であって、バイナリのものではない。そして**あらゆる書き込みの
「入力」であって「出力」ではない**。ゲートは両側にある:

- **ボードがバイナリより新しい** → read も write も拒否する（id `schema-too-new`・**exit 3**）。直すのは
  バイナリ側（CI なら `sync-task-status.yml@vX.Y.Z` の pin を上げる）。寛容にパースすれば、そのボードを
  **誤読**する——知らないフィールドが「無い」かのように振る舞い、その欠けた像のまま並べ替え・絞り込み・
  クローズしてしまう。**破壊**はもうしない（下の[未知キーの passthrough](#未知のキーは捨てずに保持する)が
  保持する）が、**保持することは理解することではない**——だからこのゲートは残る。
- **ボードがバイナリより古い** → **読めるが read-only**。write は id `schema-upgrade-required`・
  **exit 2** で拒否される（古いのはボード側で、明示コマンドで直せる＝バリデーション扱い）。`meta.json`
  が無い（shard はあるのに未 stamp の）ボードも同じ扱い。

どちらのエラー封筒にも `"details": {"board_schema": N, "binary_schema": M}` が載り、**exit code だけで
どちら側が古いか分かる**（3 = バイナリが古い / 2 = ボードが古い）。

したがって**通常のコマンドは副作用でボードを移行しない**。`meta.json` に版が stamp されるのは、
本当に空のストアを作るとき（`furrow init`）だけ。版を上げる唯一の手段が `furrow upgrade` で、これは
**flag day** である——実行後、それより古い furrow はそのボードに**書けなくなる**（古い release に
pin した CI を含む）。furrow は他 repo の pin を見られないので、**順序は人間が守る**:

```sh
furrow board                # schema:   v3 (board) / v4 (binary) — READ-ONLY: run `furrow upgrade`
# 1. 新レイアウトを載せた furrow を release する
# 2. 全 caller の sync-task-status.yml@vX.Y.Z pin（と workflow の furrow-version 既定値）をそれへ上げる
furrow upgrade              # 3. まず preview（どのストアが・何 shard 変わるか）
furrow upgrade --yes && furrow sync
```

**standalone ボード**（`standalone = true`・[standalone](#standalone-リモート無しのローカルボード)参照）には調整すべき fleet が無いので、`furrow upgrade` は flag-day チェックリストと `furrow sync` 手順を省く（単一マシンには pinned CI も remote も無い）。ゲート自体は不変で、変わるのは案内の文面だけ。

`furrow board` は三つ組（`schema_version`＝ボードの申告値・`binary_schema_version`・`schema_state`
＝`current`/`outdated`/`too-new`/`unreadable`・`writable`）を出し、**版が食い違っても失敗せず「報告」する**
——ボードとバイナリが噛み合わないときに**唯一まだ答えられるコマンド**だからで、同梱の task-status
workflow はこれを pre-flight に使い、id ごとの謎の「task not found」を N 個出す代わりに、両方の版と
対処（この repo の pin を上げよ）を名指しした 1 つのエラーで落ちる。`furrow lint` はその間
`schema-outdated` を **warn**（error にしない——read-only なボードは flag day の正当な途中経過であって、
全 repo の CI を赤くしてよい状態ではない）。

これは傷跡である: ゲート以前は `Save` が**バイナリの**版で `meta.json` を毎回 stamp していたため、
未 release の source build から回した**ただ 1 回の `furrow sync`** が共有中央ボードを 3 → 4 へ移行させ、
fleet の pin 済み release が一斉にボードを失った（v0.6.1 は全 id が "task not found"、v0.7.0 は exit 3）。
`furrow upgrade` に**降格（downgrade）はない**——戻すならボード repo の `git revert` である。

### 未知のキーは捨てずに保持する

上のゲートが火を吹くのは、誰かが版を**上げたとき**だけである。将来の furrow がフィールドを足して版を
**上げなかったら**——「追加なだけだから安全」に見えるから——`meta.json` は v4 のままで、どのゲートも
鳴らない。そして古いバイナリは shard を読み、知らないキーを落とし（`encoding/json` の寛容な unmarshal
がそうする）、次の save でその欠損を書き戻す。**通常の write 1 回、フィールド 1 個の消滅、エラーなし。**

だから、そうしない。furrow は**知らないトップレベルのキーを退避し、そのまま書き戻す**（既知キーの後ろに
sorted で）。対象は機械が書く 3 種すべて——task shard・`repos/` の review shard・`meta.json`。古いバイナリは
未来のフィールドを、見つけたときのまま返す。対にして言えば: **ゲートは「上げられた」レイアウトの誤読を
防ぎ、passthrough は「上げられていない」レイアウトの破壊を防ぐ。**

限界は 4 つ。どれも隠さない:

- **遡及しない。** `v0.9.0` までの release は今も write で未知キーを破壊する。共有ボードが安全になるのは
  **全ての書き手**（各 repo の pin 済み `sync-task-status.yml@vX.Y.Z` CI を含む）がこれを持ってから。
  最後の pin がこの release を越えるまでは、フィールド追加のたびにレイアウト版を上げ続けること。
- **トップレベル限定。** 既知のネストしたオブジェクト（`checklist` の要素）の中の未知キーは今も落ちる。
  公開スキーマもそう言っている: トップレベルの 3 オブジェクトは `"additionalProperties": true`（furrow 自身が
  知らないキーを正当に書くのだから、`false` は自分の出力を invalid と呼ぶ嘘になる）で、
  `$defs/checklistItem` は `false` のまま。
- **保持は尊重ではない。** 古いバイナリは未来の `"blocked": true` を忠実に運びながら、そのタスクを
  `furrow next` に出し、クローズもさせる。passthrough が下げるのは「静かなデータ損失」から
  「静かな意味的誤動作」へ——本物の改善だが（損失は回復不能、誤動作はバイナリ更新で直る）、
  「動作を拒否する」と言えるのはレイアウト版だけ。`furrow lint` が **`unknown-shard-key`** を warn するのは、
  この「運ばれているが無視されている」状態を可視化するため。
- **手編集のタイポは永久に残る。** キーを打ち間違えれば（`"lables"`）furrow は永久に保持する——
  理解できないキーを勝手に消すことこそ、passthrough が直している当のバグだからで、誰も掃除してくれない。
  `furrow lint` が指摘するので、消すのは自分の手編集で。**shard は furrow が書くもので、あなたが書くものではない**
  もう 1 つの理由。

なお「そのキーは既知か？」の判定には **`strings.EqualFold`**（`encoding/json` 自身の Unicode simple
case-**folding**）を使う。`strings.ToLower` は**別の関数**で、json と双方向にズレ、どちらのズレも
データ破壊になる（`"statuſ"` U+017F は json が `status` に食わせるのに ToLower は畳まない＝キーが
二重化してレーンが固着する。`"İd"` U+0130 は ToLower が `id` に畳むのに json は畳まない＝キーが消える）。

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
  └─ internal/cli   (cobra アダプタ・唯一の presentation 層)
        └─ internal/app   (唯一の mutation funnel)
              ├─ internal/config        (config.toml ロード・clamp-don't-reject)
              ├─ internal/store/fsstore (FS に触る唯一の package)
              ├─ internal/store/memstore (in-memory fake)
              └─ internal/gitrepo       (`furrow sync` 用の git subprocess アダプタ)
                    └─ internal/core  (純ドメイン・stdlib のみ)
```

- **`internal/core`** — 純ドメイン。`Index` / `Task` 構造体、唯一の `core.MarshalTask` 経路、`Store` / `Clock` などの port（interface）、validate、index 操作を持つ。**標準ライブラリしか import しない**（cobra・os・filepath は禁止）。
- **`internal/config`** — `config.toml` を読むだけ。clamp-don't-reject。
- **`internal/store/fsstore`** — **FS に触る唯一の package**。atomic write（同一ディレクトリの tmp + rename）、本文の lazy load、ランダム id 生成（`NextID`、共有カウンタなし）。
- **`internal/store/memstore`** — in-memory の fake（テスト・dry-run 用）。
- **`internal/gitrepo`** — `furrow sync` の背後にある git subprocess アダプタ（コマンド組み立て＋エラー分類だけの薄い wrapper）。`internal/app` からのみ駆動され、ストアのファイルには触れない（FS は fsstore の専権のまま）。
- **`internal/app`** — **唯一の mutation funnel**。CLI は必ずここを経由する。frozen id・正準順・closed 打刻・body↔shard の対応をここで一括管理する。
- **`internal/cli`** — cobra アダプタ。furrow の唯一の presentation 層。TUI/GUI は CLI/JSON 契約経由で駆動する別立てのフロントエンド（このリポには含まれない）。
- **`internal/schema`** — JSON Schema のソース。`internal/version` — ビルドバージョン（リンカ注入。from-source は `dev`）。

---

## standalone: リモート無しのローカルボード

会社マシンなど、共有トラッカー repo を作れない環境での定番構成: ボードを**1 台のマシン上・自前の git・push しない**で持つ（`furrow sync` も CI も無し）。[ストレージ](#ストレージハイブリッド)の挙動は共有ボードと完全に同じで、単に sync しないだけ。あなたとコーディングエージェントの双方が迷わないよう、config は 2 段だけ用意する。

1. **ボードを、コードリポから ignore した専用 git に置く。** コードの隣のワークスペースディレクトリに自前の `git init`（remote 無し）を切ると、ボードの履歴がコードリポを汚さない:

   ```
   <code-repo>/                     # 自身は remote あり（例: github.com/acme/app）
   ├── .git/info/exclude    →  claude_workspace/     # ボードをコードリポから除外
   └── claude_workspace/            # 自前の `git init`・remote 無し・push しない
       └── .furrow/
           ├── config.toml          # standalone = true
           └── meta.json, tasks/, bodies/
   ```

2. **user-level config に登録し、checkout の中から解決できるようにする。** サブディレクトリのボードはコードから上に辿っても見つからない（見つかるのは*コード*リポの git）ので、明示的にスコープする —— [中央ボード](#user-level-configper-repo-ファイル不要)と同じ `[[board]]` の仕組み:

   ```toml
   # ~/.config/furrow/config.toml
   [[board]]
   path   = "/abs/path/to/<code-repo>/claude_workspace/.furrow"
   scopes = ["/abs/path/to/<code-repo>"]   # この下で `furrow` を叩くと必ずこのボードに解決
   repo   = "auto"                          # 新規タスクを checkout の owner/repo で自動タグ
   ```

そのうえでボードの `config.toml` に **`standalone = true`** を置く（[設定](#設定furrowconfigtoml)参照）。これは**文面だけを変え、挙動は一切変えない**: `furrow upgrade` が共有ボード前提の flag-day チェックリストと「`furrow sync` して publish」案内を落とす —— 単一マシンのボードには調整すべき pinned CI も publish 先の remote も無いから。書き込みゲート・スキーマ・on-disk フォーマットは共有ボードとバイト単位で同一。

コードリポの外の完全に別ディレクトリ（例: `~/furrow-boards/app/.furrow`）でも同じ 2 段 config で動く（`path`/`scopes` を変えるだけ）。

---

## 設定（`.furrow/config.toml`）

`furrow init` がテンプレートを書き出す。furrow は **READ するだけ**で、書き換えない。方針は **clamp-don't-reject**：不明なキーは無視し、範囲外の値は安全な既定へ丸めたうえで `furrow lint` が警告する（タイプミスでツールが壊れない）。

```toml
# standalone = false          # ローカル単独ボード（remote / `furrow sync` / CI 無し）。
                              # true で `furrow upgrade` が共有ボード前提の flag-day 文面を落とす
[lanes]
order = ["inbox", "backlog", "ready", "in-progress", "waiting", "done", "icebox"]
default = "inbox"           # `furrow add` が割り当てるレーン
done = "done"               # `furrow done` の移動先（closed を打刻）
terminal = ["done", "icebox", "waiting"]  # 常に着手不可のレーン（done/parked）。next に出るレーンは下の [next].lanes

[next]
lanes = ["ready", "in-progress"]  # `furrow next` が「着手できる」とみなすレーン（依存チェックは別途）。
                                  # intake/planning レーンは除外 — 全て見たければ非 terminal レーンを列挙する

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
theme = "auto"              # フロントエンドの表示設定: auto | dark | light

[alias]                     # よく使うフィルタに名前を付ける。`furrow <name> …` が git 風に展開
triage = "ls -s inbox,backlog"   #   `furrow triage -r app` → `furrow ls -s inbox,backlog -r app`
wip    = "ls -s in-progress"      #   残りの引数は末尾に append（既存の flag/scope/auto-filter がそのまま合成）
```

`[lanes].order` は status の enum 兼ソート順位を兼ねる。`NO_COLOR` は `[ui].theme` の値に関係なく常に尊重される。

`[alias]` はよく使うコマンド文字列に名前を付ける機能。`furrow <name> <追加引数>` を git 風に展開（alias のトークンが name を置換し、残りの argv を末尾に append）するので、flag・board scope・auto-filter が全部そのまま効く。**board** の config に置く（user-level でなく）ので board と一緒に sync され、全マシン/agent で共有される。実コマンドが常に勝つ —— builtin（`ls`・`next` …）を shadow する alias は inert で `furrow lint` が警告する（`alias-shadow`）。空の alias 値は clamp 警告付きで drop。global flag は git 同様 alias の**後**に置く（`furrow triage --json`）。

`standalone = true` はローカル単独ボード（remote / `furrow sync` / CI 無し）を表す。**文面だけを変え**、挙動・スキーマゲート・on-disk フォーマットは一切変えない: `furrow upgrade` が共有ボード前提の flag-day チェックリストと `furrow sync` publish 案内を落とす（調整すべき fleet が無いから、単独運用者を誤誘導するだけ）。既定は `false`（共有ボード）。[standalone](#standalone-リモート無しのローカルボード)を参照。

---

## Claude Code 連携

furrow の連携層は意図的に薄い。**MCP も plugin も作らない**——素の CLI がそのままエージェント・インターフェースだからである: 全 read コマンドの `--json`/`--ndjson`、機械処理できるエラー封筒、そして clone できるプレーンテキストのストア（本文はエージェントが直接編集してよい）。daemon や第二のプロトコルを足しても、新しい能力は何も増えず運用面だけが増える（詳しい理由は [`docs/non-goals.md`](docs/non-goals.md)）。連携面は `CLAUDE.md` の短いブロックと `--json` だけ。

Claude（やエージェント）に守らせるルール:

- **`tasks/<id>.json` と `meta.json` を手編集しない。** 単一のマーシャラが所有しており、手編集は git の churn を生む。`add` / `move` / `reorder` / `retitle` / `done` / `check` などのコマンドで変更する（タイトル変更は `retitle`——シャードと body 見出しを一括更新）。`meta.json` の `schema_version` を上げられるのは `furrow upgrade` だけで、他のどのコマンドも触らない。
- **`bodies/*.md` は編集してよい。** 長文の散文はここに置く。
- 状態変更は必ずコマンド経由。出力を機械処理するなら `--json` / `--ndjson` を使う。
- **書く前に `furrow board --json` を pre-flight する。** `writable` / `schema_state` で分岐する（`schema-upgrade-required` = exit 2・ボードが古い / `schema-too-new` = exit 3・バイナリが古い）。ボードの版を勝手に上げるコマンドは存在しない——それは `furrow upgrade`（flag day）の仕事である。

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
    uses: akira-toriyama/furrow/.github/workflows/sync-task-status.yml@v0.10.0
    secrets:
      PROJECTS_WRITE_PAT: ${{ secrets.PROJECTS_WRITE_PAT }}
```

workflow は**自身の tag と一致する furrow release バイナリ**を DL する
（checksum 検証つき）— workflow と binary の版がズレることは構造的にない。CI の
更新は pin を上げた時だけ。認証は fine-grained PAT 1 本（`PROJECTS_WRITE_PAT`:
tracker repo の Contents Read & write のみ）。未設定の間は job は green のまま
スキップ（dormant）。検証は非ブロッキング: 不正な id/lane は報告されるが merge は止めない。

この pin こそがボードの upgrade で壊れる当のものなので、workflow は**スキーマを pre-flight する**:
tracker に対して `furrow board --json` を実行し、`.writable != true` なら、両方の版と対処
（この repo の pin を上げよ）を名指した 1 つの annotation で hard-fail する —— pin 済みの古い
バイナリが全 id に「task not found」を返すのを眺めることにならない。だから
[レイアウト版ゲート](#レイアウト版ゲート書き込みを止めるのは版が違うとき)の順序（release → 全 caller の
pin を上げる → **その後に** `furrow upgrade`）は任意ではない。

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

furrow は **CLI 専用**。core（一級の `repos`・board layout v5・両側 version gate＝新しいボードは read 拒否／古いボードは write 拒否、版を上げるのは `furrow upgrade` だけ）・config・store・app・CLI（`repo`・draft・`-r` スコープ・`apply`・`sync`・`upgrade` 含む）・`migrate` が動作する（テストは core + store + app + cli + migrate）。TUI/GUI は CLI/JSON 契約経由で furrow を駆動する**別立ての**（予定の）フロントエンドで、この binary の一部ではない。リリースは GoReleaser → Homebrew tap で公開する（[Releases ページ](https://github.com/akira-toriyama/furrow/releases) 参照・task-status Action は `v0.5.0` から同梱・一級の `repos` は `v0.6.0` から・board layout v4 は `v0.8.0` から・board layout v5 は `v0.10.0` から）。将来（低優先）: 別立てフロントエンドの TUI/GUI、read-only の Web ビューア。

## ライセンス

MIT © akira-toriyama
