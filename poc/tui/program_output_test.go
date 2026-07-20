package main

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The tests in e2e_test.go drive a real tea.Program but then assert on
// m.View() — a method the TEST calls itself. Nothing in the suite looked at the
// bytes the PROGRAM wrote to its output, so the entire render path (View ->
// renderer -> terminal) was unasserted: a program that never drew a single cell
// would have passed all 55 tests. These close that hole by reading the output
// buffer that e2e_test.go's run() declares, passes to tea.WithOutput, and drops.

// stripANSI removes ALL escape sequences, not just SGR.
//
// It is now the same grammar the package's own ansiStrip implements, and
// TestStrippersAgreeOnCursorSequences pins that. They did NOT always agree:
// ansiStrip used to end a sequence only on 'm' (SGR) or 'K' (erase-line), which
// is everything lipgloss puts in View().Content and therefore everything
// `-dump -plain` has to handle — but real terminal output interleaves cursor
// positioning (\x1b[6;31H), and a stripper still hunting for an 'm' ate the live
// text between one sequence and the next SGR. On the frames below that silently
// deleted "furrow board" and every task id. Harmless where it was used, fatal
// for anyone who pointed it at a captured stream, so it was widened rather than
// documented.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[': // CSI: params, then a final byte in @..~
			i++
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
			i++
		case ']': // OSC: runs to BEL or ST
			i++
			for i < len(s) {
				if s[i] == 0x07 {
					i++
					break
				}
				if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		default: // two-byte escape
			i++
		}
	}
	return b.String()
}

// runRaw boots a real program and returns exactly what it wrote to its output.
func runRaw(t *testing.T, w, h int, script ...string) string {
	t.Helper()
	var in bytes.Buffer
	for _, s := range script {
		in.WriteString(s)
	}
	in.WriteString("q")

	var out bytes.Buffer
	if _, err := tea.NewProgram(New(newMockProvider()),
		tea.WithInput(&in), tea.WithOutput(&out),
		tea.WithoutSignals(), tea.WithWindowSize(w, h),
	).Run(); err != nil {
		t.Fatalf("program: %v", err)
	}
	return out.String()
}

// The canary the suite was missing: if the board stopped rendering — View
// returning "", the compositor dropping every card layer, the renderer never
// being flushed — every other test would still pass, because they all render by
// calling m.View() by hand. This one only passes if the PROGRAM drew the board.
func TestProgramActuallyRendersTheBoardToItsOutput(t *testing.T) {
	out := stripANSI(runRaw(t, 140, 40))

	if len(out) < 500 {
		t.Fatalf("the program wrote %d visible chars; it did not draw a board", len(out))
	}
	for _, want := range []string{
		"furrow board",   // the title bar
		"Board", "Table", // the view tab strip
		"24 tasks", // the counter
		// The lane headers, in their DISPLAY form. The model keeps the furrow
		// slug (`in-progress`) — that is what the filter grammar and
		// `furrow set --status` take — and only the header renders it the way a
		// human reads it, the way GitHub shows the single-select option value.
		"Inbox", "Backlog", "Ready", "In progress",
		"╭", "╰", // at least one card was actually drawn
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendered frame is missing %q", want)
		}
	}

	// Lane headers alone are not a board. Require real cards: a chrome-only
	// frame (every card layer dropped) still contains all of the above.
	var seen int
	for _, id := range []string{"t-ehk7", "t-n2fc", "t-jv3j", "t-r7wr", "t-9m2q", "t-fw2m"} {
		if strings.Contains(out, id) {
			seen++
		}
	}
	if seen < 4 {
		t.Errorf("only %d fixture task ids reached the terminal; the cards are not rendering", seen)
	}
}

// bubbletea v2 turned AltScreen/MouseMode into View fields re-asserted every
// render. That is only true if they reach the wire — assert the actual DEC
// private-mode sequences, not the struct field the model handed back.
func TestProgramNegotiatesAltScreenAndMouseOnTheWire(t *testing.T) {
	raw := runRaw(t, 140, 40)

	for seq, what := range map[string]string{
		"\x1b[?1049h": "enter alt screen",
		"\x1b[?1002h": "enable cell-motion mouse tracking",
		"\x1b[?1006h": "enable SGR extended mouse coordinates",
	} {
		if !strings.Contains(raw, seq) {
			t.Errorf("the program never sent %q (%s)", seq, what)
		}
	}
	// AllMotion (1003) has worse tmux/mosh support and buys only button-less
	// hover, which drag does not need. It must never be requested.
	if strings.Contains(raw, "\x1b[?1003h") {
		t.Error("the program requested AllMotion (1003); CellMotion (1002) is the house choice")
	}
	// The window title is a View field too.
	if !strings.Contains(raw, "furrow board (POC)") {
		t.Error("the window title never reached the terminal")
	}
}

// step is one scripted key plus the condition that must appear in the program's
// OUTPUT before the next key is fed.
type step struct {
	keys  string
	what  string            // human name of the condition, for the failure message
	until func(string) bool // nil = feed the next key immediately
}

// gatedIO is the program's input AND output, coupled: a scripted key is held
// back until the program's own output shows that the previous one landed.
//
// Why this and not a plain buffer or a sleep. Everything runRaw writes lands in
// one buffer, so `q` is drained immediately behind `M` and the program quits
// before the renderer's frame timer ever fires — no frame is flushed between the
// toggle and the exit, and the only mouse negotiation on the wire is the startup
// one, which happens before a single byte of input is read. (An earlier version
// of this test asserted "a run that presses M never sends 1002h" and passed only
// because that startup render happened to lose the race on the machine it was
// written on.) A fixed sleep fixes that in isolation and goes flaky the moment
// the rest of the suite loads the machine.
//
// Waiting on mere output GROWTH is not enough either: the program writes its
// startup frame before reading a byte, so "the buffer got bigger" is satisfied
// by bytes that have nothing to do with the key. The gate therefore waits for
// the specific EFFECT, which makes it a bounded wait for the thing being
// asserted rather than a guess about scheduling. A gate that times out is
// reported as a failure, so the suite can never hang on one.
type gatedIO struct {
	mu    sync.Mutex
	cond  *sync.Cond
	out   bytes.Buffer
	steps []step
	i     int
	buf   string
	limit time.Duration

	timedOut []string
	done     chan struct{}
}

func newGatedIO(steps []step) *gatedIO {
	g := &gatedIO{steps: steps, limit: 2 * time.Second, done: make(chan struct{})}
	g.cond = sync.NewCond(&g.mu)
	// A periodic broadcast so a wait notices its own deadline even when the
	// program has gone completely quiet.
	//
	// It ticks until the run is over, NOT for a fixed number of iterations: a
	// counted loop is a live-for-N-seconds timer, and the second gate in a
	// two-gate script outlived it and blocked on cond.Wait() forever with
	// nothing left to wake it. (Found by mutation-testing this very file — the
	// deliberately broken build hung instead of failing.)
	go func() {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-g.done:
				return
			case <-tick.C:
				g.cond.Broadcast()
			}
		}
	}()
	return g
}

func (g *gatedIO) stop() { close(g.done) }

func (g *gatedIO) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, err := g.out.Write(p)
	g.cond.Broadcast()
	return n, err
}

func (g *gatedIO) Read(b []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for len(g.buf) == 0 {
		if g.i >= len(g.steps) {
			return 0, io.EOF
		}
		// Satisfy the PREVIOUS step's gate before handing over this key.
		if g.i > 0 {
			prev := g.steps[g.i-1]
			if prev.until != nil {
				deadline := time.Now().Add(g.limit)
				for !prev.until(g.out.String()) {
					if !time.Now().Before(deadline) {
						g.timedOut = append(g.timedOut, prev.what)
						break
					}
					g.cond.Wait()
				}
			}
		}
		g.buf = g.steps[g.i].keys
		g.i++
	}
	n := copy(b, g.buf)
	g.buf = g.buf[n:]
	return n, nil
}

func (g *gatedIO) String() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.out.String()
}

// runGated boots a program whose scripted keys are gated on its own output, and
// fails the test for any gate that timed out.
func runGated(t *testing.T, w, h int, steps ...step) string {
	t.Helper()
	gio := newGatedIO(append(append([]step{}, steps...), step{keys: "q"}))
	defer gio.stop()

	if _, err := tea.NewProgram(New(newMockProvider()),
		tea.WithInput(gio), tea.WithOutput(gio),
		tea.WithoutSignals(), tea.WithWindowSize(w, h),
	).Run(); err != nil {
		t.Fatalf("program: %v", err)
	}
	gio.mu.Lock()
	missed := append([]string(nil), gio.timedOut...)
	gio.mu.Unlock()
	for _, what := range missed {
		t.Errorf("timed out waiting for %s to reach the program's output", what)
	}
	return gio.String()
}

func atLeast(sub string, n int) func(string) bool {
	return func(out string) bool { return strings.Count(out, sub) >= n }
}

// The `M` toggle is the escape hatch for the terminal's own text selection, and
// it is worthless unless MouseMode actually reaches the wire. Because MouseMode
// is a per-render View field in bubbletea v2, "reaches the wire" means the
// renderer diffs one frame's mouse mode against the last and emits the
// transition mid-run — not merely the reset every shutdown performs.
func TestMouseToggleReachesTheWire(t *testing.T) {
	const (
		enable  = "\x1b[?1002h"
		disable = "\x1b[?1002l"
	)

	// Baseline: exactly one negotiation, at startup, and one reset, at
	// shutdown. A run that never toggles must not thrash the mouse mode.
	on := runGated(t, 140, 40)
	if n := strings.Count(on, enable); n != 1 {
		t.Errorf("baseline enabled mouse tracking %d times, want exactly 1", n)
	}
	if n := strings.Count(on, disable); n != 1 {
		t.Errorf("baseline disabled mouse tracking %d times (only the shutdown one "+
			"is expected), want 1", n)
	}

	// Counting a SINGLE M proves nothing: the renderer emits the disable when
	// the mode flips, and the teardown then skips its own because tracking is
	// already off — so one M and no M both leave exactly one disable on the
	// wire, just in different places.
	//
	// M TWICE is the assertion that cannot be faked. Off-then-on can only put a
	// SECOND enable on the wire by way of a frame rendered while the program was
	// still running, which is exactly the claim being made.
	again := runGated(t, 140, 40,
		step{keys: "M", what: "the mouse-off frame (1002l)", until: atLeast(disable, 1)},
		step{keys: "M", what: "the mouse-on frame (a second 1002h)", until: atLeast(enable, 2)},
	)
	if n := strings.Count(again, enable); n != 2 {
		t.Errorf("M twice put %d enables on the wire, want 2 (startup + the toggle "+
			"coming back on); tracking never round-tripped through a live frame", n)
	}
	if n := strings.Count(again, disable); n != 2 {
		t.Errorf("M twice put %d disables on the wire, want 2 (the toggle going off "+
			"+ the shutdown)", n)
	}
}

// A drag driven as real SGR bytes must still be drawn: the ghost has to reach
// the terminal, not just the model. This is the render-side twin of
// TestE2EMouseDragAcrossColumns, which only checked where the task ended up.
func TestDragGhostIsRenderedByTheProgram(t *testing.T) {
	const w, h = 140, 40
	probe := boardModel(t, w, h)
	src := probe.lay.Col("backlog")
	dst := probe.lay.Col("ready")
	grab := src.Cards[0]
	id := src.Tasks[grab.Idx].ID

	// Press and move, but never release — the drag is still in flight when the
	// frame carrying the ghost is drawn.
	out := stripANSI(runRaw(t, w, h,
		mousePress(grab.X+3, grab.Y+1),
		mouseMotion(dst.X+3, dst.Top+2),
		mouseMotion(dst.X+4, dst.Top+3),
	))

	if !strings.Contains(out, "DRAG "+id) {
		t.Error("the status bar never announced the drag on the terminal")
	}
	if n := strings.Count(out, id); n < 2 {
		t.Errorf("%s was drawn %d times; want a shadow AND a ghost on the wire", id, n)
	}
}

// The regression this pins is real and was shipped: a stripper that terminates
// a sequence on the first 'm' it sees treats \x1b[6;31H as unterminated, keeps
// hunting, and swallows the 'm' of the WORD "moved" — deleting live text,
// silently, mid-word. Both strippers must handle the whole CSI/OSC grammar.
func TestStrippersAgreeOnCursorSequences(t *testing.T) {
	cases := map[string]string{
		// SGR around text, then a cursor move: the classic corruption.
		"\x1b[31mred\x1b[0m\x1b[6;31Hmoved": "redmoved",
		// Erase-line, private-mode set/reset, and an OSC window title.
		"a\x1b[2Kb\x1b[?1002hc\x1b]2;title\x07d": "abcd",
		// A bare two-byte escape.
		"x\x1bMy": "xy",
	}
	for in, want := range cases {
		if got := ansiStrip(in); got != want {
			t.Errorf("ansiStrip(%q) = %q, want %q", in, got, want)
		}
		if got := stripANSI(in); got != want {
			t.Errorf("stripANSI(%q) = %q, want %q", in, got, want)
		}
	}

	// And the property that matters for every width assertion in the suite: a
	// stripped frame must contain no escape byte at all.
	frame := ansiStrip(boardModel(t, 140, 40).View().Content)
	if strings.ContainsRune(frame, 0x1b) {
		t.Error("a stripped frame still carries an escape byte")
	}
}
