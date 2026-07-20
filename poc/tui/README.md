# furrow kanban TUI — proof of concept

A GitHub-Projects-shaped kanban board for furrow's task model, in a terminal.
Built on **bubbletea/v2 + bubbles/v2 + lipgloss/v2**, using lipgloss v2's native
**layer compositor** for z-ordered overlays (side-peek, drop indicator, drag
ghost).

> **This is a POC on a throwaway branch.** furrow itself stays **CLI-only and
> charm-free**. `poc/tui` is a **separate Go module** on purpose, so no charm
> dependency can ever reach furrow's core. A real TUI would be an out-of-repo
> front-end (**ridge**) driving furrow through its CLI/JSON contract — this
> module is a feasibility study, not a component.

```
cd poc/tui
go run .                                   # the real thing (needs a terminal)
go run . -dump -w 140 -h 40                # one frame to stdout, no TTY needed
go run . -dump -w 140 -h 40 -plain         # …without ANSI, so it diffs
```

---

## What it proves

| Claim | Where |
|---|---|
| A GH-Projects visual grammar fits a 140-column terminal — columns with counts, value/effort sums, WIP badges, chip-laden cards, horizontal lane scrolling | `view.go`, `card.go` |
| GitHub's keyboard **move mode** (lift → place → commit / restore) is *more* natural in a TUI than in a browser | `model.go` (`enterMove`/`onMoveKey`) |
| A reorder maps exactly onto furrow's real data model: one sparse `priority` integer, respacing only when the gap runs out | `board.go` (`MoveTo`/`respace`) |
| Mouse drag-and-drop across columns works under `MouseModeCellMotion`, with a z-99 ghost and a z-50 drop indicator | `drag.go`, `view.go` |
| Dependencies become legible with **no DAG layout engine** | `peek.go`, `deps.go` |
| CJK titles render correctly — every card line is measured, never `len()`-counted | `card.go`, `layout_test.go` |
| Columns share the terminal the way GitHub's do — negotiated width, card gutters, column containers, coloured labels and per-lane dots | `layout.go`, `theme.go`, `view.go` |
| The frame is never larger than the terminal, down to 1×1 and including negative sizes | `view.go` (`fitFrame`), `adv_hunt_test.go` |

## What it fakes

- **The data is hardcoded.** `fixture.go` is a generated in-memory copy of **24
  real tasks** from `akira-toriyama/projects` (the `vista` epic `t-fw2m`, its 18
  children, and their dep targets) — real titles, real Japanese, real
  dependency graph, real bodies. The POC **never reads or writes a `.furrow`
  store**; mutations live and die with the process. `r` reloads the fixture and
  discards them.
- **`Provider` is the seam.** Every mutation the UI performs goes through
  `Provider` (`provider.go`). A real implementation shelling out to
  `furrow --json` / `furrow set --before` drops in without the UI changing.
  Only `mockProvider` exists here.
- **`e` (edit body)** really does launch `$EDITOR` via `tea.ExecProcess`, on a
  temp file, and writes the result back to the **in-memory** board only.
- **The body renderer is a 40-line markdown-ish styler**, not glamour — `-dump`
  has to stay deterministic and a second wrapping engine would disagree with
  lipgloss about CJK widths.

---

## Keymap

Every mouse gesture has a keyboard twin. That is the contract, not a nicety: a
terminal user may be on a mouse-less tmux.

### Navigation

| Key | Action |
|---|---|
| `←` `↓` `↑` `→` / `h` `j` `k` `l` | move between cards and columns |
| `tab` / `shift+tab` | next / previous column |
| `g` / `G` | top / bottom of the column |

### Moving a card — the centrepiece

| Key | Action |
|---|---|
| `enter`, `m` or `shift+space` | **enter move mode** — the card visibly detaches (double border) |
| `←` `↓` `↑` `→` *(in move mode)* | place the card, live, with a drop indicator |
| `ctrl+↑` / `ctrl+↓` *(in move mode)* | to the top / bottom of the destination column |
| `ctrl+←` / `ctrl+→` *(in move mode)* | to the first / last lane |
| `enter` *(in move mode)* | commit |
| `esc` *(in move mode)* | cancel — original lane, position **and cursor** restored |
| `shift+K` / `shift+J` | quick reorder within the lane, no mode |
| `[` / `]` | cycle the card one lane back / forward |

`shift+space` and the `ctrl+arrow` extremes are GitHub Projects' own documented
board shortcuts. All of them go through `commitMove`, the same single mutation
path the mouse drop uses.

### Detail & dependencies

| Key | Action |
|---|---|
| `space` | open / close the detail side-peek |
| `t` | toggle the transitive dep-tree overlay inside the peek |
| `ctrl+d` / `ctrl+u` | scroll the peek |
| `>` | **jump to the first unfinished blocker** (pushes the jump stack) |
| `<` | pop the jump stack |
| `b` | toggle "only blocked" |

### Filter

| Key | Action |
|---|---|
| `/` | focus the filter input (live as you type; `enter` applies, `esc` reverts) |
| `esc` | clear the filter (when one is applied and no overlay is open) |

Grammar — a GitHub-Projects subset:

```
lane:ready            status: is an alias
repo:vista            substring, so it matches akira-toriyama/vista
label:ui
type:epic
id:t-jv3j   parent:t-fw2m
is:blocked | is:actionable | is:done | is:open | is:epic | is:stuck | is:draft
no:<field> / has:<field>   field ∈ repo label dep parent body checklist value effort
-lane:done            leading "-" negates the whole token
lane:inbox,backlog    comma inside ONE token is OR
<bare word>           case-insensitive substring of title or id (CJK works)
```

Separate tokens **AND**. An unparseable token is **reported in the filter bar
and skipped** — the rest of the query still applies, because you type a filter
one character at a time.

### Everything else

| Key | Action |
|---|---|
| `d` | close the task into the done lane |
| `x` | toggle the first unfinished checklist item *(POC shortcut)* |
| `e` | edit the body in `$EDITOR` |
| `r` | reload the fixture (discards session edits) |
| `v` | board ⇄ table view |
| `M` | **toggle mouse tracking** — the text-selection escape hatch |
| `?` | full help overlay |
| `q` / `ctrl+c` | quit |

### Mouse

| Gesture | Action |
|---|---|
| click a card | select it |
| drag a card | move it within or across columns (ghost follows the cursor, drop indicator shows the landing slot) |
| drag to a column's top/bottom edge | the column auto-scrolls, and keeps scrolling while the pointer is parked there |
| release **off the board** | cancel — the gutter, the chrome rows and the area past the last column are all "not a drop target" |
| `esc` mid-drag | cancel; the release that follows is swallowed |
| wheel over a column | scroll that column (only as far as there is something below the fold) |
| wheel over the peek | scroll the peek |

The **release** decides where a card lands, not the last lane the pointer
happened to cross. Yanking a card away from the board and letting go is the
universal escape hatch, so it has to cancel.

---

## GitHub Projects fidelity

Applied, in rough order of impression delta:

| # | Change |
|---|---|
| 1 | **A gutter between cards** — without it `╰──╯` sat on the next `╭──╮` and the stack read as one ruled table rather than a deck of discrete draggable objects |
| 2 | **Column containers** — an inset background running the column's full height, so an empty lane still reads as a lane (and as a drop target) instead of as blank screen |
| 3 | **Coloured label chips**, hashed from the label name so a label keeps its colour everywhere |
| 4 | **Card field order**: title → labels → footer metadata, GitHub's order (id above the chips read as a log entry) |
| 5 | **Count next to the lane name**, in a pill, instead of flush-right 22 cells away |
| 6 | **Per-lane colour dot**, so you find "Done" without reading |
| 7 | **A real placeholder** in the filter bar — it used to print a full example query, which read as an *active* filter while the header said "24 tasks" |
| 8 | **`Board │ Table` tab strip** — `v` toggled the table view with no on-screen affordance at all |
| 9 | **Thick border on the selected card** — hue alone is invisible in `-plain`, on a 16-colour TTY, and to a colourblind reader |
| 10 | **A bracketed dashed drop caret** (`▸╌╌╌◂`) instead of a solid hairline that looked exactly like the column rule |
| 12 | **`v0 e0` suppressed** under empty lanes |
| 13 | **`shift+space` and `ctrl+arrow`**, GitHub's documented move shortcuts |
| 14 | **Display-cased lane names** ("In progress"); the model keeps the `in-progress` slug |
| 15 | **Negotiated column width** — columns share the terminal instead of sitting at a fixed 28, which also fixed the overflow on narrow terminals |

Skipped, with reasons:

- **Swimlanes / horizontal grouping (#16)** — a structural rewrite, not a
  surgical change. The largest remaining fidelity gap.
- **Dynamic footer height (#11)** — `footerH` is a const consumed by the layout,
  the peek, the table and the scroll arithmetic; threading a dynamic value
  through all of them to reclaim one row is churn against every geometry test.

---

## The three questions this was built to answer

**Q1 — "マウスで DnD がいい / GitHub Projects の Kanban のような動きが理想"**
Answered by `drag.go` + the compositor overlays. Cross-column and within-column
drag both work, with a grab-offset ghost so the card does not snap under the
cursor, and a drop indicator that is deliberately given **no layer id** so
`Compositor.Hit()` skips it. Two headless tests drive it with synthetic SGR
bytes. Edge auto-scroll is a `tea.Tick` repeat armed while the pointer sits in a
column's hot zone, so holding still at the edge keeps scrolling (a terminal only
emits motion events when the pointer *moves*, so without the tick it scrolled
once and stopped). The **release** decides the landing lane, so pulling a card
off the board cancels.

**Q2 — "キーボードでの task 入れ替えは現実的なら優先で"**
Answered, and it is the better half. GitHub's move mode transplants to a TUI
almost too well: `enter` lifts, arrows place, `enter` commits, `esc` restores.
Crucially it is **the same arithmetic as the drag** (`commitMove` is the single
mutation path), and it maps 1:1 onto furrow's `set --status --before/--after`.
`shift+K/J` covers the common nudge without any mode at all.

**Q3 — dependency legibility**
All three requested layers, plus the stretch goal:

1. **Resolved bidirectional lists** in the peek — `blocked by` and `blocks`,
   each id resolved to *title + lane* with `o` open / `v` done / `?` not-on-board.
   Reverse edges exist nowhere on disk; they are computed. Measured over the
   real 658-task board this is max 8 rows, mean 2.6 — it always fits, so no
   layout engine is needed.
2. **Blocked glyph on the card** (`x` + blocker count, dimmed) and a `b`
   filter toggle. *Sink, don't hide*: a blocked task is marked, never dropped.
3. **Jump-to-blocker with a jump stack** (`>` / `<`). This is the one dep
   feature a static drawing cannot do, and the only interactive one any TUI has
   shipped. The real board's longest chain is 5 edges, so two presses reach any
   root blocker. If the blocker is hidden by the current filter it is **pinned**
   into view rather than being a dead end.
4. *(stretch)* **`t` — a transitive dep tree** via `lipgloss/v2/tree`, both
   directions, depth-capped at 4, cycle-safe, done-subtrees elided. `tree` has
   no multi-parent support, so a node reachable twice is **drawn twice and
   flagged `↩seen`** rather than merged — measured cost on the real board is
   ~2.5% extra rows.

**Not built, deliberately:** a Sugiyama / rail 2-D DAG layout. No Go library
provides one, and the median live dep component is 2 nodes.

---

## Known gaps — the honest list

Nothing here is a mystery; each one is a decision or a missing feature, not a
suspected bug. Everything the adversarial pass found *was* a bug and is fixed
(see *Things that bit*).

### Missing features

- **No swimlanes / parent grouping.** GitHub's "group by field → horizontal
  sections" is the largest remaining structural gap. Children are listed in the
  peek and `ls --tree`-style hierarchy is not a board view at all. Not a POC
  defect — a different layout engine.
- **`x` toggles only the first unfinished checklist item.** A real client would
  put a cursor in the checklist. POC shortcut.
- **No horizontal scroll in the table view**; long titles truncate.
- **The peek is not resizable**, and it overlaps the board rather than pushing it.
- **The body renderer is a ~40-line markdown-ish styler**, not glamour —
  deliberate, so `-dump` stays deterministic and a second wrapping engine cannot
  disagree with lipgloss about CJK widths.
- **Card titles cap at 3 lines**; the full title is in the peek.
- **The footer is always two rows** (status + short help). GitHub has none, and
  the row could be reclaimed when the status line is empty — but `footerH` is a
  const consumed by the layout, the peek, the table and the scroll arithmetic,
  and threading a dynamic value through all of them for one row is churn against
  every geometry test. Left alone deliberately.

### Behaviour worth knowing

- **Mouse tracking fights the terminal's own text selection.** While tracking is
  on, the terminal hands drags to the app, so click-drag no longer selects text
  for copy/paste. Two ways out:
  - Press **`M`** to toggle tracking off (and again to turn it back on). This is
    free in bubbletea v2 because `MouseMode` is a per-render `View` field, not a
    program option.
  - Hold the terminal's **bypass modifier** while selecting:

    | Terminal | Modifier |
    |---|---|
    | xterm, Ghostty, tmux, most Linux terminals | **Shift** |
    | iTerm2 | **Option / Alt** |
    | Apple Terminal.app | **Fn** |

- **Below a column's last *rendered* card means the end of the LANE**, not the
  end of the visible run. With nothing below the fold the two are identical;
  with cards hidden they differ, and the older reading made the bottom of a long
  column unreachable by any gesture.
- **A drop into the slot a card already occupies is a no-op** — nothing is
  written and `updated` is not stamped, because furrow treats positional
  bookkeeping as not-progress. The status line says so rather than claiming a
  reposition.
- **Below ~24 columns the board renders a single column** as wide as the
  terminal. It never overflows, but it is not usable; a real client would show a
  single-lane list.

---

## Architecture

```
                LOC
doc.go           34  package doc: what it proves / fakes / is not
main.go          99  flags, -dump, -demo states, program boot
provider.go      55  the Provider seam + the in-memory mock
board.go        353  Task/Lane/Board, sparse-priority MoveTo + respace, AdjustDropIndex
deps.go         247  THE one definition of blocked/actionable/reverse/container/stuck/tree
filter.go       239  the typed query parser + matcher
fixture.go      407  generated: 24 real tasks with their real bodies
model.go        717  state, Update, normal-mode keys, cursor, filter, jump stack
movemode.go     214  move mode + commitMove, the single mutation path for a reorder
drag.go         322  the mouse drag state machine (threshold, drop target, auto-scroll)
editor.go        42  $EDITOR via tea.ExecProcess
layout.go       343  measured geometry, memoised card heights, hit-testing
card.go         192  one card: CJK-safe wrapping, coloured label chips, glyphs
view.go         365  board frame: chrome, column containers, drop caret, ghost, help
peek.go         329  detail side-peek, resolved dep lists, dep tree, prose renderer
table.go        155  the flat table view + the ANSI stripper
theme.go        177  palette, per-label hues, per-lane dots, glyphs (light/dark)
keys.go         110  key bindings + help keymap
                ---
                4400 non-test, of which 407 generated

deps_test.go     237   filter_test.go   206   layout_test.go   225
move_test.go     366   view_test.go     259   e2e_test.go      452
adv_hunt_test.go 532   adv_hunt2_test.go 351  adv_hunt3_test.go 272
adv_hunt4_test.go 153   program_output_test.go 392
                ---
                3445 test  (102 passing test functions/subtests)
```

The `adv_hunt*` files are an adversarial pass: each test was written to FAIL
against an earlier build and to name one concrete defect. They are kept as
regression tests, which is why several of them read like accusations.

Three rules hold the thing together:

1. **`deps.go` is the only definition** of blocked / actionable / reverse-deps /
   container / stuck. The card glyph, the filter's `is:blocked`, the peek and
   the tree all read it, so they cannot drift.
2. **`commitMove` is the only mutation path** for a reorder. Move mode,
   `shift+J/K` and a mouse drop all land there, so the two index translations
   (`AdjustDropIndex` for the remove-then-insert boundary, `boardInsertIndex`
   for filtered columns) are applied exactly once, in one place.
3. **Geometry is measured, never predicted.** `cardHeight` renders the card and
   measures it rather than counting lines, because those y positions go straight
   to the mouse hit-test — a predicted height that is one row off makes cards
   accept drops aimed at their neighbours. It is memoised per frame
   (`layout.measurer`): honest measurement inside `scrollToShow`'s candidate loop
   is otherwise O(n²) full card renders on a path that runs on every event.
4. **One frame is never larger than its terminal.** Every piece clamps itself,
   and `fitFrame` is the backstop — a compositor grows its canvas to fit any
   layer, so one oversized child used to expand the whole frame.

### Things that bit, recorded so they do not bite again

**lipgloss / bubbles**

- **`lipgloss.Style.Width(n)` / `Height(n)` are TOTALS** — border and padding
  included. Assuming they were content-sized silently re-wrapped one line per
  card, which made every measured height a lie and sheared the columns below.
- **The compositor NORMALISES negative coordinates** by shifting the whole scene,
  rather than clipping. A status bar placed at `Y(m.h-2)` on a 1-row terminal is
  `Y(-1)`, and the frame came back **6 rows tall with the help bar on row 0**.
  Clamp layer coordinates to the canvas; do not hand the compositor a negative.
- **The compositor also grows its canvas to fit an oversized layer**, so a
  28-cell card on a 20-column terminal widened the entire frame. Hence
  `fitFrame`'s `MaxWidth`/`MaxHeight` backstop.
- **`bubbles` `help.FullHelpView` ignores `SetWidth`** and lays every group on
  one row — 98 cells regardless of terminal width. The overlay packs groups into
  as many rows as fit.

**bubbletea v2**

- **`key.WithKeys(" ")` compiles and never matches.** `key.Matches` compares
  `Key.String()`, which renders the space bar as `"space"`. Every non-letter
  binding is now pinned by `TestKeyBindingsMatchTheirRealKeyStrings`, driven with
  the message a terminal actually sends — including `shift+space` and the four
  `ctrl+arrow` bindings, which are the newest members of that trap.
- **`tea.KeyMsg` is an *interface* in v2** matching press *and* release. In a
  type switch it must come after `case tea.KeyPressMsg:` or it swallows presses.
- **Do not re-read `msg.Button` on motion/release.** Some terminals do not report
  it. The drag records the button at press time. (There is a regression test that
  feeds `MouseNone` motion events.)
- **A bare `"\x1b"` cannot drive Esc from a byte buffer.** A real terminal's Esc
  is disambiguated from an escape *sequence* by **timing**; a buffer has none, so
  `"\x1b"+"q"` parses as `alt+q` and the test hangs forever. The tests use
  `"\x1b[27u"` — the Kitty-protocol CSI-u encoding of Escape — which decodes
  unambiguously with no timing.

**Interaction logic**

- **A sticky drop target commits the wrong move.** `dropLane` used to be whatever
  lane the pointer last crossed, so releasing on the title bar — or in the empty
  area past the last column — still moved the card. The RELEASE decides, and a
  release that is not inside a column's card band cancels.
- **Manhattan distance is the wrong drag threshold.** `dx=1,dy=1` scores 2 and
  armed a real drag, and a diagonal one-cell twitch is the commonest accidental
  mouse movement. Chebyshev (`max(|dx|,|dy|)`) treats every one-cell neighbour as
  a click.
- **"esc restores" has to mean the CURSOR too.** Move mode's `followDrop` walks
  the selection along with the drop target, so cancelling without restoring it
  left the next `d` / `x` / `enter` acting on a different task.
- **Two owners for one card.** `onMouseDown` refused to start a drag during a
  keyboard move, but not the reverse — pressing `m` mid-drag armed both, and the
  release plus the following `enter` committed two moves.
- **`ensureVisible` must not be blanket-called from `Update`**, or it re-asserts
  "the cursor must be visible" right after the wheel scrolled a column and the
  wheel appears dead. The scroll offset is instead clamped to what is actually
  scrollable, per frame, in `buildLayout` — which also repairs a stale offset in
  an *unfocused* lane, something `ensureVisible` could never reach.
- **A modal input owns `esc`.** Checking `cancelDrag()` first let a still-armed
  drag eat the `esc` meant to dismiss the filter, leaving it modal with no way
  out but `enter`.
- **"Reported but not fatal" has to mean the board still shows something.**
  `is:bogus` kept the term, matched nothing, and emptied all 24 tasks while the
  filter bar politely called it a warning. Unknown values are dropped; the rest
  of the term survives.
- **String surgery on a query corrupts it.** `ReplaceAll(raw, "is:blocked", "")`
  turns `-is:blocked` into a bare `-`, which parses as a bare-word term. Token
  removal, not substring removal.

**Testing**

- **A stripper that ends a sequence on the first `m` eats live text.** Given
  `\x1b[6;31Hmoved` it keeps hunting for a terminator, finds the `m` of the WORD
  "moved", and deletes the text in between — silently, mid-word. `ansiStrip` now
  handles the whole CSI/OSC grammar.
- **Asserting on a program's output is a race unless you wait for the output.**
  A scripted key and the `q` behind it land in the same buffer, so the program
  can quit before the renderer's frame timer ever fires; the mouse-toggle
  assertion passed only because the startup render happened to lose that race,
  and a fixed sleep went flaky the moment the rest of the suite loaded the
  machine. `gatedIO` holds each key back until the program's own output shows the
  previous one landed. Output *growth* is not enough — the startup frame is
  written before a byte of input is read.
- **A bounded wait needs an unbounded waker.** The first version of that gate
  used a counted broadcaster goroutine, which expired mid-run and left the second
  gate blocked on `cond.Wait()` forever. Found by mutation-testing the toggle:
  the deliberately broken build **hung** instead of failing. It now ticks until
  the run ends, and a timed-out gate is reported as a test failure.

---

## Verification — all headless, no terminal in the loop

```sh
gofmt -l .                              # empty
GOTOOLCHAIN=local go build ./...
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go test ./... -count=1
GOTOOLCHAIN=local go test -race ./... -count=1
```

102 test functions/subtests pass, `-race` included.

Coverage:

- **filter parser** — grammar shapes, AND/OR/negation, CJK bare words, and that
  `is:blocked` agrees exactly with the graph.
- **dep resolution** — blocked/actionable/reverse, unknown deps block, container
  is declared not inferred, `stuck` walks through sub-epics, progress roll-up,
  cycle-safe tree, DAG duplication flagged.
- **move index arithmetic** — `AdjustDropIndex` in a table test, the end-to-end
  off-by-one it prevents, sparse priority vs. respace, respace does not advance
  neighbours' `Updated`, and `boardInsertIndex` under a filter.
- **geometry** — every glyph is single-width, every rendered card line is exactly
  the card width for every fixture task in every card state, measured height ==
  rendered height, hit-test round-trips (including that the inter-card gutter is
  *not* a card), and the frame never exceeds `w × h` — checked down to **1×1** and
  at negative widths, in four view states.
- **the render path itself** (`program_output_test.go`) — the bytes the PROGRAM
  wrote, not a `View()` the test called: the board reaches the terminal, alt
  screen / SGR mouse / window title are negotiated on the wire, `1003h`
  (AllMotion) is never requested, the drag ghost is drawn, and the `M` toggle
  round-trips through a live frame.
- **adversarial regressions** (`adv_hunt*_test.go`) — drop outside the board,
  drop on the chrome, diagonal twitch, double ownership, move-mode cancel,
  no-op drop not stamping `updated`, drops into a filtered-empty lane, scroll
  clamping in focused and unfocused lanes, dropping below the fold, `esc` routing
  under a modal filter, negative-Y chrome, tiny and negative terminal sizes, dep
  cycles / self-deps / parent cycles, CJK column alignment at every width from 56
  to 160, and a differential test of the move arithmetic over **every**
  `(fromLane × fromIdx × toLane × dropIdx)` on the real fixture against a naive
  remove-then-insert reference.
- **end-to-end, driving a real `tea.Program`** (`e2e_test.go`):
  - *keyboard*: boot → move mode → arrows → commit, and the board mutated;
    same-lane reorder; `esc` restores lane **and** order; peek + filter; jump to
    blocker and back; `d`; `[`/`]`; `M` toggling `View().MouseMode`.
  - *mouse*: synthetic SGR bytes (`\x1b[<0;X;YM` press, `\x1b[<32;X;YM` motion,
    `\x1b[<0;X;Ym` release) through `tea.WithInput` — cross-column drag,
    within-column reorder, the **drag threshold** (a 1-cell twitch selects and
    does **not** move), `esc` cancel + swallowed release, button-less motion, and
    wheel scrolling.

### Eyeballing a frame without a TTY

```sh
go run . -dump -w 140 -h 40 -plain                     # the board
go run . -dump -w 140 -h 40 -plain -peek               # + detail side-peek
go run . -dump -w 140 -h 40 -plain -tree               # + transitive dep tree
go run . -dump -w 140 -h 24 -plain -table              # the table view
go run . -dump -w 140 -h 30 -plain -demo move          # mid move-mode
go run . -dump -w 140 -h 30 -plain -demo drag          # mid drag: ghost + shadow
go run . -dump -w 140 -h 30 -plain -demo help          # the help overlay
go run . -dump -w 140 -h 40 -plain -filter 'is:blocked'
go run . -dump -light ...                              # light palette
```

`-demo` exists because a drag and a lifted card only exist *mid-gesture*: without
it, "does the drop indicator render?" would be a question only a human at a
terminal could answer.
