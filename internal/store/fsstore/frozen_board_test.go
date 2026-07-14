package fsstore

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// The frozen board: a REAL board's bytes, committed under testdata/, that a
// Load → Save round-trip must reproduce exactly.
//
// Every other determinism test in this repo builds its fixture with the code under
// test, so both sides of the comparison move together: change the marshaller and
// the shard shape at once, and they all stay green. The bytes here were written by
// an earlier furrow and are checked in, so they cannot move with the code — which
// is what makes this the guard for the case t-ye3c is about: **a shard field added
// without bumping `core.SchemaVersion`.**
//
// What it catches that nothing else does:
//
//   - A new NON-omitempty field: every shard on every board grows `"newfield": null`
//     on the next ordinary write. TestShardFieldsGolden sees the struct change and
//     can be waved through with -update-fields; only a frozen board shows the actual
//     damage — fleet-wide churn, and a silent drop by every older binary.
//   - A REMOVED or renamed field: the on-disk key becomes unknown, so the
//     passthrough parks it and re-emits it AFTER the known keys. The data survives,
//     but the key ORDER changes — invisible to any in-memory test.
//   - The byte recipe drifting through the STORE: 2-space indent, no HTML escaping,
//     the trailing newline, and where extras are spliced — asserted here on files,
//     not on a Go value.
//   - `meta.json`'s bytes, which no other committed golden covers at all.
//   - Save writing a shard it did not need to (git churn): the mtimes must not move.
//
// Regenerating is deliberate and visible: `go test ./internal/store/fsstore
// -run TestFrozenBoard -update-board` rewrites the fixture, and git then shows a
// rewritten board in the diff. If you are doing that because the shard's shape
// changed, `core.SchemaVersion` must go up in the same change (see CLAUDE.md's
// flag-day rule) — preserving a field an old binary does not know is not the same
// as it HONOURING one.
var updateBoard = flag.Bool("update-board", false, "rewrite testdata/frozen-board from this binary's output (only alongside a deliberate schema bump)")

const frozenBoardDir = "testdata/frozen-board"

func TestFrozenBoardRoundTripsByteIdentical(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".furrow")
	copyTree(t, frozenBoardDir, root)
	before := snapshotTree(t, root)

	s := New(root, lanes, "t-", 5)

	// The three machine-written file kinds, each through its own writer — a Save
	// alone would only ever rewrite tasks/.
	idx, err := s.Load()
	if err != nil {
		t.Fatalf("Load the frozen board: %v", err)
	}
	if err := s.Save(idx); err != nil {
		t.Fatalf("Save the frozen board: %v (a board at the binary's own layout must be writable)", err)
	}
	repos, err := s.ListRepos()
	if err != nil {
		t.Fatal(err)
	}
	for i := range repos {
		if err := s.SaveRepo(&repos[i]); err != nil {
			t.Fatal(err)
		}
	}
	// The idempotent raise: a board already at this layout must come out unchanged,
	// extras and all (this is the path `furrow upgrade` runs).
	if err := s.SetBoardVersion(core.SchemaVersion); err != nil {
		t.Fatal(err)
	}

	after := snapshotTree(t, root)

	if *updateBoard {
		copyTree(t, root, frozenBoardDir)
		t.Log("frozen board rewritten — review the git diff, and make sure core.SchemaVersion went up if the shard's shape did")
		return
	}

	// The file SET first: a Save that invents or deletes a file is as much a
	// regression as one that garbles a byte.
	if !sameKeys(before, after) {
		t.Fatalf("Load→Save changed the board's file set:\n before %v\n after  %v", keysOf(before), keysOf(after))
	}
	for _, name := range keysOf(before) {
		b, a := before[name], after[name]
		if !bytes.Equal(b.data, a.data) {
			t.Errorf("%s is not byte-identical after Load→Save:\n%s\n"+
				"Every board in the fleet would be rewritten this way on its next ordinary write. If you changed a "+
				"persisted field, `core.SchemaVersion` must go up in the SAME change — an old binary PRESERVES a key it "+
				"does not know, but it does not HONOUR it (CLAUDE.md, the flag-day rule). Once the bump is in, "+
				"regenerate this fixture with -update-board.", name, firstDiff(b.data, a.data))
			continue
		}
		// A no-op Save must not touch a single file: writeIfChanged is what keeps a
		// routine `furrow sync` from churning every shard in git.
		if !b.mtime.Equal(a.mtime) {
			t.Errorf("%s was rewritten with identical bytes (mtime moved %v → %v) — a no-op save must write nothing, or every sync churns the whole board",
				name, b.mtime, a.mtime)
		}
	}
}

// firstDiff renders the first line where the frozen bytes and the rewritten bytes
// part company, with a little context — the whole file would bury the one line
// that matters.
func firstDiff(frozen, rewritten []byte) string {
	fl := strings.Split(string(frozen), "\n")
	rl := strings.Split(string(rewritten), "\n")
	for i := 0; i < len(fl) || i < len(rl); i++ {
		f, r := lineAt(fl, i), lineAt(rl, i)
		if f == r {
			continue
		}
		var b strings.Builder
		for j := max(0, i-2); j < i; j++ {
			fmt.Fprintf(&b, "  %s\n", lineAt(fl, j))
		}
		fmt.Fprintf(&b, "- %s   (line %d, frozen)\n", f, i+1)
		fmt.Fprintf(&b, "+ %s   (line %d, this binary)\n", r, i+1)
		return b.String()
	}
	return "(files differ only in trailing bytes)"
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "(end of file)"
}

// The half the round-trip cannot see on its own: the unknown keys must actually
// have been PARKED (not merely coincidentally re-emitted), and they must be
// reported, because a carried-but-ignored field is a semantic trap, not a feature.
func TestFrozenBoardParksUnknownKeys(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".furrow")
	copyTree(t, frozenBoardDir, root)
	s := New(root, lanes, "t-", 5)

	idx, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	task, i := idx.Find("t-frzn1")
	if i < 0 {
		t.Fatal("the frozen board must still carry t-frzn1")
	}
	if got := task.ExtraKeys(); !equalStrings(got, []string{"blocked", "blocked_by"}) {
		t.Errorf("t-frzn1's unknown keys must be parked for lint to report: got %v", got)
	}
	if plain, j := idx.Find("t-frzn2"); j < 0 || len(plain.ExtraKeys()) != 0 {
		t.Errorf("t-frzn2 has no unknown keys and must park none: got %v", plain.ExtraKeys())
	}
	meta, err := s.LoadMeta()
	if err != nil {
		t.Fatal(err)
	}
	if got := meta.ExtraKeys(); !equalStrings(got, []string{"written_by"}) {
		t.Errorf("meta.json's unknown key must be parked too: got %v", got)
	}
	repos, err := s.ListRepos()
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || !equalStrings(repos[0].ExtraKeys(), []string{"last_bot_reviewed"}) {
		t.Errorf("the repo review shard's unknown key must be parked: got %+v", repos)
	}
}

// snapshot is one file's committed bytes and the mtime it had before the save.
type snapshot struct {
	data  []byte
	mtime time.Time
}

// snapshotTree reads every file under root, keyed by its slash-form relative path.
func snapshotTree(t *testing.T, root string) map[string]snapshot {
	t.Helper()
	out := map[string]snapshot{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = snapshot{data: data, mtime: info.ModTime()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// copyTree copies src over dst (creating it), file by file. The fixture must stay
// read-only, so every test works on a copy.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func keysOf(m map[string]snapshot) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameKeys(a, b map[string]snapshot) bool { return equalStrings(keysOf(a), keysOf(b)) }

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
