package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every fact the human output prints is in the JSON, and the other way
// round — the eight asymmetries of t-w846.

// A review stamp was JSON-only: `show` and `epic show` print it.
func TestShowPrintsTheReviewStamp(t *testing.T) {
	initStore(t)
	id := addTask(t, "x")
	epic := addEpic(t, "box", "-r", "o/r")
	mustRun(t, "review", id)
	mustRun(t, "review", epic)
	if out := mustRun(t, "show", id); !strings.Contains(out, "\nreviewed: ") {
		t.Errorf("show lacks the reviewed line:\n%s", out)
	}
	if out := mustRun(t, "epic", "show", epic); !strings.Contains(out, "\nreviewed: ") {
		t.Errorf("epic show lacks the reviewed line:\n%s", out)
	}
}

// The `deps:` line's resolved edges (id, title, state) were human-only: the
// JSON carries them as deps_resolved beside the raw deps ids.
func TestEpicShowJSONCarriesResolvedDeps(t *testing.T) {
	initStore(t)
	gate := addEpic(t, "gate", "-r", "o/r")
	box := addEpic(t, "box", "-r", "o/r")
	mustRun(t, "epic", "dep", box, gate)
	for _, args := range [][]string{{"--json", "epic", "show", box}, {"--json", "epic", "show", box, "--no-body"}} {
		out := mustRun(t, args...)
		var v struct {
			Deps         []string `json:"deps"`
			DepsResolved []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
				State string `json:"state"`
			} `json:"deps_resolved"`
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		if len(v.Deps) != 1 || len(v.DepsResolved) != 1 || v.DepsResolved[0].ID != gate || v.DepsResolved[0].Title != "gate" || v.DepsResolved[0].State != "open" {
			t.Errorf("%v: deps = %v deps_resolved = %+v; want the gate resolved as open", args, v.Deps, v.DepsResolved)
		}
	}
}

// The due band's `outside next:` tag was human-only: each due row carries
// outside_next in JSON when next cannot reach it, and nothing otherwise.
func TestBriefJSONDueRowsCarryOutsideNext(t *testing.T) {
	freezeClock(t)
	initStore(t)
	focus := addEpic(t, "focus", "-r", "o/r")
	other := addEpic(t, "other", "-r", "o/r")
	mustRun(t, "epic", "activate", focus)
	in := addTask(t, "in focus", "-e", focus, "-r", "o/r", "-s", "ready", "--due", dayOffset(t, -1))
	outside := addTask(t, "outside", "-e", other, "-r", "o/r", "-s", "ready", "--due", dayOffset(t, -1))
	out := mustRun(t, "--json", "brief", "-r", "")
	var b struct {
		Due struct {
			Overdue []struct {
				ID          string `json:"id"`
				OutsideNext string `json:"outside_next"`
			} `json:"overdue"`
		} `json:"due"`
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("brief --json: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, r := range b.Due.Overdue {
		got[r.ID] = r.OutsideNext
	}
	if got[in] != "" || got[outside] != "box "+other {
		t.Errorf("outside_next = %v; want nothing for %s and \"box %s\" for %s", got, in, other, outside)
	}
	if human := mustRun(t, "brief", "-r", ""); !strings.Contains(human, "outside next: box "+other) {
		t.Errorf("the human tag still prints:\n%s", human)
	}
}

// boards --json told a missing board and a non-store directory apart only
// in the human line: on_disk carries the difference.
func TestBoardsJSONCarriesOnDisk(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	plain := t.TempDir() // a directory, not a store (an empty one reads as a store-to-be)
	if err := os.WriteFile(filepath.Join(plain, "README"), []byte("not a board\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Join(xdg, "furrow")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[[board]]\npath = \"" + plain + "\"\nscopes = [\"/a\"]\n\n[[board]]\npath = \"" + filepath.Join(plain, "gone", ".furrow") + "\"\nscopes = [\"/b\"]\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := mustRun(t, "--json", "boards")
	var list struct {
		Boards []struct {
			Store  string `json:"store"`
			Exists bool   `json:"exists"`
			OnDisk bool   `json:"on_disk"`
		} `json:"boards"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Boards) != 2 {
		t.Fatalf("boards --json: %v\n%s", err, out)
	}
	if list.Boards[0].Exists || !list.Boards[0].OnDisk || list.Boards[1].Exists || list.Boards[1].OnDisk {
		t.Errorf("boards = %+v; want the directory on_disk but not a store, the missing one neither", list.Boards)
	}
}

// An epic write's envelope carries session_warn like a task write's: the
// idle-occupant warning used to reach only stderr for `epic activate` and
// friends.
func TestEpicEnvelopeCarriesSessionWarn(t *testing.T) {
	initStore(t)
	armGuard(t, 20*time.Minute)
	epic := addEpic(t, "box", "-r", "o/glyph")
	for _, args := range [][]string{{"epic", "activate", epic}, {"epic", "set", epic, "--goal", "g"}, {"note", epic, "hi"}} {
		so, _, code := runSplit(t, append([]string{"--json"}, args...)...)
		if code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, so)
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(so), &env); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, so)
		}
		warn, _ := env["session_warn"].(map[string]any)
		if clashes, _ := warn["clashes"].([]any); len(clashes) != 1 {
			t.Errorf("%v: envelope lacks session_warn.clashes: %v", args, env)
		}
	}
}

// attach --json is the one-id mutation envelope every other task write
// emits — {before, after, changed} with updated advanced — plus the asset
// facts; it used to be its own {id, asset, ref, line} object.
func TestAttachJSONIsTheMutationEnvelope(t *testing.T) {
	initStore(t)
	id := addTask(t, "x")
	src := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(src, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stamps are whole seconds: run the attach "later" so updated can move.
	testClock = shiftedClock{2 * time.Second}
	t.Cleanup(func() { testClock = nil })
	out := mustRun(t, "--json", "attach", id, src)
	var env struct {
		Before  map[string]any `json:"before"`
		After   map[string]any `json:"after"`
		Changed []string       `json:"changed"`
		Asset   string         `json:"asset"`
		Ref     string         `json:"ref"`
		Line    string         `json:"line"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("attach --json: %v\n%s", err, out)
	}
	// `changed` lists shard FIELDS; a body line is prose, and its mark is the
	// stamp — so before/after differ in updated, as note's envelope does.
	if env.Before["id"] != id || env.After["id"] != id || env.Changed == nil || env.Before["updated"] == env.After["updated"] || !strings.HasPrefix(env.Asset, "bodies/assets/") || env.Ref == "" || env.Line == "" {
		t.Errorf("envelope = %+v", env)
	}
}

// shiftedClock is the wall clock moved ahead by d — for a write whose stamp
// must differ from one made in the same second.
type shiftedClock struct{ d time.Duration }

func (c shiftedClock) Now() time.Time { return time.Now().Add(c.d) }
