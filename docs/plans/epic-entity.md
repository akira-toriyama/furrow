# furrow: epic を一級エンティティにする（schema v6 / v1.0.0）

## Context

**問題**: 中央ボードは 850 task（open 233）まで膨らみ、作業が散る。`furrow next` は並べ替えを一切せず、
lane → priority → id の順に出すだけで、「今どの取り組みを進めているか」を知らない。結果、毎回盤面全体から
ROI の高い単発を拾うことになり、構造的に散る。カレーの材料を1つ買って、次に旅行の宿を取り、また別件へ飛ぶ。

**やること**: 「箱」を宣言し、**repo ごとに 1 つだけ active** にして、`furrow next` をその箱に絞る。
箱＝epic を task の一種（`type: epic` + `parent`）から**独立エンティティ**に引き上げる。

**現状の epic は task の一種**で、盤面の1行でしかない。全読みコマンドに付くスコープ軸は `-r`（repo）と
`-l`（label）だけ。epic には無い。ゴールも状態も持てない。

**期待する結果**: 人間が箱を1つ開ける → Claude はその箱の task だけを拾う → 箱が閉じる → 次を開ける。
「増える」は止まらないが（非 active な箱への追加は自由）、「散る」が止まる。

**実測（2026-07-28、中央ボード）**:
- 850 task / open 233
- `parent` を持つ 108 件のうち **107 件の親が `type: epic`** → `parent` は実質「epic 所属」そのもの
- open のうち epic-typed 10 / 親が epic 54 / **epic 未所属 169**（＝移行後に人が仕分ける量）
- ボードの `config.toml` に `[types]` ブロックは無い（既定のまま）→ 移行で container の判定を
  `type == "epic"` に決め打ちしてよい

### 同じ病気を sill の board でも実測している（2026-07-28・別セッション）

安い task は消化されるのにコアが進まない。sill の board は open 95 → 295 に増え続けている
（完了は週 100 件超あるのに）。具体形:

- `scripts/appkit-floor.sh` が main で毎回「16 構造中 14 件が DEBT」と報告しているのに、
  **その移行 task が board に 1 つも無かった**。ゲートが数えるだけで誰も減らさない
- その日に消化されたのは URL 差し替え・用語集・chore。基盤は 5 件中 1 件だけ

原因の内訳（この設計が何を直し、何を直さないかの線引き）:

1. **ROI = value/effort で、value も effort も Claude が自分で付けている。** effort は 1..5 に
   上限があるので、基盤（v5/e5 = 1.0）は雑務（v4/e1 = 4.0）に**構造的に負ける**。自己採点が
   自分の優先度を決めている
2. **`next` が container を既定で除外する。** 「次に何をするか」を決めるコマンドが、設計上いちばん
   大きい仕事を表示できない → **この変更で問題ごと消える**（epic は task ではなくなり、`next` は
   active epic の子を返す）
3. 誤解を避けるための事実: **ROI は並び順に関与していない**。`core.SortFields` は
   `updated/created/value/effort` のみで `roi` は無く、ROI はクエリと `show` の表示だけ。
   ただし `core.ROI()` の doc comment が「the signal an agent uses to pick the next task」と
   書いており、**その文言が誤用の発生源**（→ 下記「範囲外」で修正）

**この設計が直すのは「選択」だけで、「生成」は直らない。** 95 → 295 の増加は生成側の数字。

---

## 決定済み（この会話で確定）

**エンティティ**
- `.furrow/epics/<id>.json`（`.furrow/repos/*.json` と同じ形の shard）。id は `e-` プレフィックス
- フィールド: `id` / `title` / `goal` / `active` / `labels` / `repos` / `meta` / `created` / `updated` /
  `closed` / `body`
- `goal` は**閉じる条件を1行**。**任意**（空でよい。lint しない）。`epic ls` の各行に出るのが本体の価値。
  title で自明な epic には書かせない
- `body` は **task と同じ `.furrow/bodies/<e-id>.md`**（epic 専用ディレクトリを作らない。id が
  `e-`/`t-` で衝突しないので、`furrow edit` / `sync -b <id>` / `.gitattributes` の `bodies/*.md merge=union` /
  `orphan-body` lint が**そのまま効く**）。順序の理由・監査の記録・判断の経緯はここ
- `meta` は**フラットな `map[string]string`**。furrow は中身を解釈しない・検索対象にしない（jq で読む）
- 持たない: `value` / `effort` / `roi` / `priority` / epic 間の `deps` / 入れ子
- レーンに乗らない。open/closed の2値（`closed` の有無）
- 「ルール」フィールドは不採用（散文は `body` に書く）

**所属**
- task は `epic` を **0..1** 持つ。label（多対多のタグ）とは違い「所属」なので単一
- **open な task では必須**。`lint` がエラーで防ぐ（`add` は拒否しない）。terminal レーン（`Cfg.IsTerminal`）は免除

**active**
- **repo ごとに 1 つ**（ボード全体で 1 つ、ではない）。「furrow で active な epic は 1 つまで /
  sill で 1 つまで」。読みは既定で repo スコープ（`repo="auto"`）なので、repo を移るたびに
  deactivate/activate させるのは摩擦が実益を超える
- 制約の正確な形: **どの repo r についても、r を含む active な epic は高々 1 つ**。
  `repos` を複数持つ epic を active にすると**その全 repo の枠が埋まる**。実測では複数 repo の
  task は 9%（open 233 中 21）だが、その中身は fleet 系（`.github` + 対象ツール）と
  ライブラリ + 消費者（sill+wand 等）で、**epic ほどこの形になる**（現行 epic の `t-fm4x` が実例）。
  全枠を消費するのは意図どおり — fleet 移行中に片方だけ別件を進めるのを防ぐ
- `repos` が空（draft）の epic は **activate できない**（exit 2）。repo に紐づかない箱を進行中に
  できると、per-repo 制約を素通りする active がいくつでも作れる
- `epic activate` は衝突した repo 名と相手の epic id を `details` に載せて exit 2
- `furrow next` は既定で **その repo で active な epic の task ∪ epic 未所属の task に絞る**。
  `-r ''`（全 repo）では active な epic 全部の union。
  **絞る（先頭に置くだけにしない）**理由: 安い task の問題は「見えなかった」ことではなく
  「**選べた**」こと。一覧の 2 行目に effort 1 の URL 差し替えが見えている状態で 1 行目の
  effort 5 の基盤移行を選び続けられるなら、そもそもこの仕組みは要らない。順序は散文の規約に
  なるが、絞り込みなら focus を外れた瞬間に `--all-epics` / `-e` が履歴に残る（機構になる）
- **絞った集合の中では、active epic の「次の未ブロック子」を先頭に置く**（箱そのものは返さない —
  箱は actionable ではないので、返すと無視する癖がつく）
- **子が全てブロックされている時も active は維持**し、`next` が
  `focus e-xxxx: 子 N 件すべて e-yyyy でブロック` と明示する。ブロッカーを解くのがその日の 1 手
  になるので、規則が生き残る
- その repo に active が1つも無い時は **空 + exit 0**、stderr に `furrow epic ls` と `furrow lint` を案内
- **`epic done` は `active` を落とすが、次の epic は furrow が選ばない**。空のまま `lint` が
  「この repo に active な epic が無い」を警告する。選定は一番人間の判断が要る所なので機械が
  奪わない。後片付け（枠の解放）だけ機械がやる
- 切り替えは furrow の外の話。furrow は道具なので identity を見ない。規約は `projects/CLAUDE.md`。
  **禁止機構は置かない**（CLI である以上 human-only は強制できないし、必要な場面で邪魔になる）。
  代わりに **`epic activate` が切り替えを epic の body に 1 行追記**（日時＋指示の出所）し、
  `sync` の要約に出す。想定リスクは「Claude が勝手に切り替える」ではなく
  **「指示を切り替えだと誤読する」**方で、記録があれば誤読が数週間後でなく同じセッションで露見する

**削除**（同じ flag day で）
- `Task.Type` / `[types]` セクション / containers / `next --containers` / `is:container`
- `Task.Parent` / `furrow parent` / `parent-cycle` / `parent-done` / `parent-missing` /
  `dep-mirrors-children` / `unknown-type` / `has:parent` / `parent:` / `child-of:`
- `furrow migrate`（Task.md インポータ）は**触らない**。将来削除予定のため育てない

**コマンド**
```
furrow epic add <title> [--goal ...] [--meta k=v]...
furrow epic ls [--all]        # active 印 / 進捗 (d/t) / goal（空なら空欄）
furrow epic show <id>         # goal / meta / 進捗 / 所属 task をレーン別 / body
furrow epic set <id> [--goal|--meta|--title]
furrow edit <e-id>            # body を開く（既存コマンドが id 前置で epic も扱う）
furrow epic activate <id> | deactivate <id>
furrow epic done <id>         # closed を打刻し、同じ書き込みで active を落とす
furrow add "..." -e <epic> / furrow set <id>... -e <epic> / furrow ls -e <epic>
```
`-e` は**全ての絞り込み読み**（`ls` `next` `revisit` `stats` `search` `brief`）に付く。`show` には付けない。
`-q epic:` / `has:epic` / `no:epic` も追加。

**版**: schema **v6**、リリースは **v1.0.0**（現在 v0.14.0）。flag day 1回。

---

## PR 分割

**PR 1 = v1.0.0（flag day）** — 上の「決定済み」全部 + `upgrade` の v5→v6 変換 + docs 一式。
**`lint` の `sync` 相乗りと、切り替え記録の `sync` 表示も PR 1 に含める**（当初 PR 2 だったが格上げ）。
理由: 「呼ばれない lint は無い lint と同じ」。`sync` は読む前・書いた後に必ず回るので、
active が 2 つになった状態も epic 未所属も、**最長 1 セッションで露見する**。

**PR 2 以降（非スキーマ）** — `brief` 冒頭の `active: e-xxxx <title> (3/7)` 行、`epic show` の表示磨き

**出す順（flag day 規約）**: ① v1.0.0 リリース → ② `sync-task-status.yml` のピンと `furrow-version` 既定を
上げる → ③ `furrow upgrade --yes` + `furrow sync` → ④ 169 件の仕分け

---

## 実装（PR 1）

### 押さえるべき不変条件

- **単一マーシャラ経路**: `json.Marshal`/`Unmarshal` は `internal/core/{marshal,passthrough}.go` と
  `internal/cli/output.go` のみ（`scripts/check-marshal-singlepath.sh`）。`MarshalEpic`/`UnmarshalEpic` は
  `MarshalRepo` の完全な鏡にする
- **`MarshalJSON` メソッドを生やさない**（`internal/cli` が `--json` view で埋め込むため、昇格して兄弟
  フィールドが消える）
- **新フィールドは struct の末尾**（`Task.Epic`）。`TestShardFieldsGolden` が key 順を凍結している
- **`core.SchemaVersion` を通常の write 経路で名指ししない**（`scripts/check-schema-write-guard.sh`）
- `Meta` は `{}` であって `null` ではない（`[]`-not-`null` 則の拡張）。Go の `encoding/json` は map の
  キーをソートするので決定性は自動的に満たされる

### core / store / schema

| 対象 | 変更 |
|---|---|
| `internal/core/epic.go`（新） | `Epic` struct、id 機構、`EpicPath`、`canonicalizeEpic` |
| `internal/core/marshal.go` | `MarshalEpic` / `UnmarshalEpic` / `canonicalizeEpic` |
| `internal/core/passthrough.go` | `epicKnownKeys`、`(*Epic).ExtraKeys()` |
| `internal/core/task.go` | `Parent`/`Type` 削除、`Epic` を**末尾**に追加、`SchemaVersion = 6` |
| `internal/core/ports.go` | `LoadEpics` / `SaveEpic` / `ListEpicIDs` / `NextEpicID` |
| `internal/core/{index,cycles,staledep,validate}.go` | `Children`/`Ancestors`/`HasAncestor`、`ParentCycleProblems`、`ParentDoneProblems`、3つの validate ルールを削除 |
| `internal/core/epic_lint.go`（新） | 下表の 6 コード |
| `internal/core/validate.go` の `orphan-body` / `dangling-link` | epic id も既知として扱う（`bodies/` を共有するため。直さないと epic の body が全部 orphan 扱いになる） |
| `internal/store/fsstore` | `epics/` の load/save。**`SaveEpic` は `gateWrite()` を通す**（`repos/` と同じ理由）。body は既存の `bodies/` 機構をそのまま使う（新ディレクトリを作らない） |
| `internal/store/memstore` | 同じ port の実装 |
| `internal/schema/schema.go` | `EpicV1` 新設、`TaskV2` から `parent`/`type` を落として `epic` 追加、`furrow schema epic` |
| `internal/config` | `[types]` 一式削除、`[ids]` に `epic_prefix`（既定 `"e-"`） |

#### lint コード（`epic_lint.go`）

| code | 重大度 | 意味 |
|---|---|---|
| `epic-required` | error | open な task に `epic` が無い（terminal レーンは免除） |
| `epic-missing` | error | task の `epic` が存在しない epic を指している |
| `epic-closed` | warn | 閉じた epic の下に open な task が残っている |
| `epic-multi-active` | error | **同じ repo に active な epic が 2 つ以上**（別マシンで同時に activate して merge された時の後始末。per-repo に数えるだけで書ける） |
| `epic-no-active` | warn | **その repo に active な epic が 1 つも無い**。`epic done` の後、次を人が選ぶまで出続ける（機械が次を選ばないので、これが催促役） |
| `epic-id-pattern` / `epic-duplicate-id` | error | shard 名と id の不一致・重複 |

`epic-required` は v1.0.0 が着地した瞬間に**既存の open 全件が error になる**（実測 169 件）。
`upgrade` のプレビューに backfill の手順を出し、移行期は既存の `[lint].ignore_codes` で
黙らせられることを案内する（新しい config キーは足さない — 既にある逃げ道で足りる）。

### app

新規 `internal/app/epic.go`（`repo.go` + `review.go` の形）。`EpicAdd` / `EpicList` / `EpicShow` /
`EpicSet` / `EpicActivate` / `EpicDeactivate` / `EpicDone` / `ResolveEpic` / `ActiveEpic` /
`epicProgress` / `mutateEpic`。

- `EpicActivate` が **repo ごとに active 単一**を強制（衝突 repo と相手の epic id を `details` に載せて
  exit 2）。`repos` が空の epic は activate 不可
- `EpicDone` は `closed` 打刻と**同じ書き込みで `active` を落とす**（落とさないと閉じた箱が永久に
  activate を塞ぐ）。**次の epic は選ばない** — 空のまま `epic-no-active` が催促する。機械が奪って
  いいのは後片付けだけで、選定は人の判断
- `EpicActivate` は**切り替えを epic の body に 1 行追記する**（`YYYY-MM-DD HH:MM activated — <reason>`。
  `--reason` 省略時は空欄）。禁止機構を置かない代わりの記録で、`sync` の要約に「今セッションの
  切り替え」として出す。狙いは「Claude が勝手に切り替える」の抑止ではなく、**「指示を切り替えだと
  誤読した」を同じセッションで露見させる**こと（数週間後に気づくのでは遅い）
- `epicProgress` は `tree.go` の `rollupProgress`/`rollupProgressSeen` を置き換える。再帰も `seen` も
  不要（入れ子が無い）。**全 index に対して数える**（読みフィルタで箱の進捗を過少に見せない）
- `ResolveEpic` は `ResolveRepo` と同形（完全一致 → id 前置一致 → title 部分一致、曖昧なら exit 2 + `candidates`）

`QueryOpts`: `Type` と `IncludeContainers` を削除、`Epic string` / `EpicScope *string` / `AllEpics bool` を追加。
`next` の述語はこれ（`next --containers` はテストゼロで出荷された。ここは表で網羅する）:

| active | flag | task の epic | next に出る |
|---|---|---|---|
| `e-A` | — | `e-A` | ○ |
| `e-A` | — | なし | ○（救済） |
| `e-A` | — | `e-B` | × |
| なし | — | 何でも | ×（空 + exit 0 + stderr ヒント） |
| 何でも | `--all-epics` | 何でも | ○ |
| `e-A` | `-e e-B` | `e-B` | ○ |
| `e-A` | `-e e-B` | なし | ×（明示 `-e` は厳密） |

**絞った集合の中の順序**: active epic の「次の未ブロック子」を先頭に置く。**箱そのものは返さない**
（箱は actionable ではないので、返すと無視する癖がつく）。子が全てブロックされている時は active を
維持したまま、`next` が `focus e-xxxx: 子 N 件すべて e-yyyy でブロック` と明示する — ブロッカーを
解くのがその日の 1 手になるので、運用規則が生き残る。

削除: `internal/app/parent.go`、`tree.go` の container/stuck 機構、`validateTypeFilter`/`matchType`/
`unknownTypeErr`、`factsFor` の container 枝。

`revisit`: `children_done`/`stuck_container` を epic 単位の signal に置換する。**`stuck_container` は
container ごと消えるので「そのまま使える」ではなく、epic 版を新しく作る**（計算は同じ — 配下に open は
あるが actionable が無い）。signal は 3 つ:

| code | 意味 |
|---|---|
| `epic_all_done` | 子が全部 done — 閉じるか検討（旧 `children_done`） |
| `epic_stuck` | 子に open はあるが actionable が無い（旧 `stuck_container` の epic 版）。**active な epic で出た時が最重要** — `next` が空になった理由そのもの |
| `epic_stale` | **active な epic が N 日更新されていない**。`[revisit].stale_days` に乗せる |

`epic_stale` について: 要望は「active epic が **N セッション**無更新」だったが、**furrow はセッションを
数えられない**（そういう概念を持たない）。測れるのは日数だけなので既存の `[revisit].stale_days` に
乗せる。`furrow revisit` の他の signal と同じ時計になるので、閾値が 2 つに割れない利点もある。

epic の粒度は「done が 3 セッション以内」規則の**対象外**（箱は作業単位ではない）。放置の検知は
上の `epic_stale` が担当する。

### cli

- 新規 `internal/cli/cmd_epic.go`。`cmd_parent.go` は削除
- `-e` を全絞り込み読みに追加（`internal/cli/scope_test.go` の `scopedQuery` に arm を追加）
- **`root.go` の `mutatingCommands` は leaf 名で引いている** — `epic activate` は `cmd.Name()` が
  `"activate"` なので autocommit されない。**トップレベル名で引くよう直し、`"epic"` を登録する**
- `stateGlyph` から `▣`（container）を落とす
- **★ の意味が変わる**: これまで「`furrow next` が渡すものと厳密に一致」だったが、`next` が active epic
  で絞るので ★ は上位集合になる。この文言は5箇所（`ls` の Long help、`stateGlyph` の doc comment、
  `App.actionable` の doc comment、README の `ls` bullet、glossary の **actionable** 行、CLAUDE.md の
  `ls --tree` bullet）にあるので**全部書き換える**。★ を epic 対応にはしない
- 空 `next` の stderr ヒントは `-e` / `--all-epics` 指定時には出さない

### sync（lint を相乗りさせる）

`furrow sync` は読む前・書いた後に必ず回るので、ここに乗せた検査だけが**確実に呼ばれる**。
既存の `revisit` 要約行の隣に足す:

```
sync: committed=true pulled=true pushed=true conflict=false
revisit: 1 dep_done, 0 stale, 1 epic_stuck (furrow) — furrow revisit
lint:   2 errors (epic-required 2) — furrow lint
epic:   activated e-k3m9「旅行の準備」 14:32 — 指示: <reason>      ← 今セッションに切り替えがあった時だけ
```

- lint は**件数とコード名だけ**（全文は `furrow lint`）。sync を遅くしないよう、既存の lint 経路を
  そのまま呼んで数えるだけにする
- lint が error を返しても **sync は exit 0 のまま**。sync の仕事は board の公開であって検査ではなく、
  ここで落とすと「lint が赤いと board が同期できない」という最悪の結合ができる
- 切り替え行は epic の body から今セッション分を拾う（記録の出口）

### upgrade（v5 → v6 変換）

1. `type == "epic"` の task を epic エンティティへ（title→title、labels/repos を維持、done/icebox の
   ものは **closed** epic に、`active` は全て false）
2. その epic を親に持つ `parent` エッジを `epic` 所属へ（実測 107 本）
3. epic でない親を持つ 1 件は手で
4. **body は `bodies/t-xxxx.md` → `bodies/e-yyyy.md` に rename するだけ**（1,395 行、無損失）。
   `goal` は**空で作る** — 本文から機械的に導出しない（先頭段落が閉じる条件とは限らない）。
   人が後から `furrow epic set <id> --goal` で埋める
5. task 側に残る本文中の `[[t-xxxx]]` リンクが epic を指していたものは `[[e-yyyy]]` に書き換える
   （書き換えないと `lint` の `dangling-link` が 18 件出る）
6. 冪等。`--yes` 無しではプレビュー（`t-xxxx "<title>" → e-yyyy` / 所属が移る task 数 / body の rename）

### docs（ここが分量の半分）

`scripts/check-docs-vocab.sh` の claims 表が、閉じた語彙を散文の region に対応付けている。今回動くのは
`commands`（`epic` 追加・`parent` 削除）/ `config-keys`（`[types]` 削除）/ `lint-codes` / `revisit-codes` /
`revisit-summary-keys` / `query-qualifiers` / `query-presence` / `query-is` の**全部**。対応 region は
README.md・CLAUDE.md・docs/architecture.md・docs/non-goals.md・`internal/app/revisit.go` の doc comment。

加えて:
- `scripts/gen-command-table.sh` を再実行して README の生成表を更新（手編集禁止）
- `scripts/check-readme-parity.sh` が `{"schema_version": N}` リテラルを `core.SchemaVersion` に固定して
  いる → README と docs/*.md の該当箇所を **6** に
- `scripts/check.sh` に epic schema の diff を1行追加
- 無ガードの散文も掃く: `docs/architecture.md`（container ゲート・`Tree` 設計・`[types]` 表・v6 の根拠）、
  `docs/glossary.md`（`type` / `container / epic` / `parent` / typed query / epic 行）、
  `docs/non-goals.md`（コマンド一覧・milestone の扱い）、`docs/scheduling.md`（revisit コード一覧）
- `internal/config/template.go` の `[types]` ブロック削除 → repo ルート `config.toml` と byte 一致させる

---

## 実装順

1. `core/epic.go` → `marshal`/`passthrough` → epic のテストを**先に緑にする**（ここまでは単独で証明できる）
2. `ports.go` + `fsstore` + `memstore`（`var _ core.Store = (*Store)(nil)` が漏れをコンパイル時に捕まえる）
3. **削除**（`Type` / `Parent` / 関連 core API）→ 上位が一斉に壊れる。コンパイラが app/cli の作業を列挙する
4. `Task.Epic`（末尾）、`epic_lint.go`、`lintCodes` / `RevisitCodeList` の登録更新
5. `SchemaVersion = 6` + v6 の根拠コメント
6. `internal/schema` → `docs/schema/*.json` を**バイナリから再生成**（const のバイトそのものを commit）
7. `core/migrate_v6.go` + `internal/app/upgrade.go`
8. app（`epic.go` → `QueryOpts` → `Next` → `revisit` → `lint`）→ cli（`cmd_epic.go` → `-e` → 出力）
9. `testdata/shard-fields.golden` を `-update-fields` で再生成
10. **frozen board を再生成**（`-update-board`）。`t-frzn1.json` の `parent` が未知キーとして末尾へ回るので
    key 順が変わり、`meta.json` も 5 → 6。`epics/e-xxxx.json`（未知キー入り）を fixture に追加し、
    `frozen_board_test.go` に `SaveEpic` の pass を足す。**この commit の diff が flag day そのもの**
11. docs 一式 + `sh scripts/check.sh`

3〜7 の間は `go build ./...` が赤い。それ以外は緑を保つ。

---

## 検証

```sh
sh scripts/check.sh            # これ1本。マーシャラ/schema write ガード、build/vet/test、
                               # golangci、schema/config/docs ドリフト、CLI スモーク、release dry-run
```

加えて、headless の実弾を使い捨てボードで:

```sh
tmp=$(mktemp -d) && cd "$tmp" && git init -q .
furrow init
furrow epic add "旅行の準備" --goal "パンフレットを作る" --meta '期間=2026/08/10 ~ 08/15' --meta 場所=北海道
furrow epic add "カレー"                # goal 省略 = 空。lint は何も言わない（任意フィールド）
furrow edit <e-id>                      # TTY 無しなら bodies/<e-id>.md のパスを印字（task と同じ機構）
furrow add "宿の予約" -e 旅行
furrow next --json                     # [] + exit 0 + stderr にヒント（まだ active でない）
furrow epic activate <e-id>
furrow next --json                     # 宿の予約 が出る
furrow epic add "カレー" --goal "..." && furrow epic activate <e2>   # exit 2 を確認
furrow add "無所属" && furrow lint --json | jq '.[]|select(.code=="epic-required")'
furrow epic show <e-id> --json         # goal / meta / progress / tasks
```

**移行の実弾は本番ボードのクローンで**（`projects` を別ディレクトリに clone → `furrow upgrade`
（`--yes` 無し）→ プレビューを目視 → `--yes` → `furrow lint` が 169 件を並べることを確認 → 破棄）。
本番へ流すのは v1.0.0 リリースと CI ピン更新の**後**。

「直したことの証明」は、修正を無効化してテストが落ちることまで確かめる。特に:
- `EpicActivate` の単一チェックを外すと `epic_test.go` が落ちる
- `next` の epic フィルタを外すと `epic_scope_test.go` の表が落ちる
- `EpicDone` の active クリアを外すと「閉じた箱が activate を塞ぐ」回帰テストが落ちる

---

## やらないこと（PR 1 の範囲外）

- burndown / 燃焼グラフ / 速度予測（`stats` と `scripts/burndown.sh` の領域）
- active の個数制限を config 化（今は repo あたり 1 固定。要ると分かってから config キー 1 個で
  足せる。bump 不要）
- epic 間の `deps`、epic の入れ子、epic の archive
- `meta` の検索・集計（jq で読む）
- furrow 側で「誰が叩いたか」を見ること。**禁止機構も置かない** — CLI である以上 human-only は
  強制できないし、必要な場面で邪魔になる。代わりに記録する（→ active の節）

### 生成側（この設計では直らない・別途）

この設計が直すのは**選択**だけ。安い task が今の速度で作られ続ける限り board は増え続ける
（sill: open 95 → 295）。`projects/CLAUDE.md` には既に「`effort ≤ 2` かつ今触っているコードの中なら
task 化せずその PR で直す」があり、**守られていない**。

furrow 側で縛るなら候補は「**active epic があり・新 task がその子でなく・`effort ≤ 2` のとき
`add` が warn**」。最低でも件数を数えて締めで報告できるようにする。ただし PR 1 には入れない —
選択側が効き始めてから、生成側の数字が実際どう動くかを見て決める（先に両方入れると、どちらが
効いたのか分からなくなる）。

### `core.ROI()` の doc comment（別 PR で先に出す）

実物が「the signal an agent uses to pick the next task」と書いている（`internal/core/task.go`）。
比率を**スケジューラ**として使うことを推奨してしまっており、これが誤用の発生源。ROI が正しいのは
「独立した項目で余った容量を埋める」場面で、**部分完成の価値がほぼゼロな基盤作業には当てはまらない**
（14 個中 13 個 SwiftUI 化しても床ポリシーは未達）。フィルタとしての用途に限定する文言へ。

doc comment だけの非破壊変更なので、この長寿命ブランチに載せず **main から小さい PR で先に出す**。

### value の尺度（furrow ではなく projects 側）

`projects/CLAUDE.md` は effort をセッション換算で定義しているが value は無定義＝付ける側の裁量。
自己採点が自分の優先度を決めている状態の半分はここ。furrow のスキーマの話ではないので別途。

### 運用規則（`projects/CLAUDE.md` 側・furrow の外）

道具だけでは効かないので、規約側にこれが要る。**この PR には含まれない**が、依存関係として記録する。

- **毎セッション、active epic を 1 手進める。進められなければ理由を報告する。**
- **active の切り替えは人間の指示で行う。** Claude は自発的に切り替えない。切り替えたい場合は
  要望として出す（そもそも動機がほぼ発生しないので稀なはず）。
- furrow 側は禁止しない・記録する。規約が守られたかは、`sync` に出る切り替え行と
  `epic-no-active` / `epic_stale` で事後に見える。
