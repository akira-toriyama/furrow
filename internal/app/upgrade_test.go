package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// outdatedBoard inits a real board on disk, then walks its meta.json back one
// layout — the state the operator's board is in whenever furrow's HEAD has moved
// ahead of the release that last wrote it.
func outdatedBoard(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	a, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("a task", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(dir, DirName, "meta.json")
	if err := os.WriteFile(meta, []byte("{\n  \"schema_version\": 3\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return a, meta
}

// The default is a preview: it reports the flag day it WOULD perform and writes
// nothing. (`archive`'s guard — a destructive op never fires without --yes.)
func TestUpgradeDryRunWritesNothing(t *testing.T) {
	a, meta := outdatedBoard(t)
	before, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}

	rep, err := a.Upgrade(false)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Changed || rep.Applied {
		t.Errorf("dry run = %+v, want changed:true applied:false", rep)
	}
	if rep.From != 3 || rep.To != core.SchemaVersion {
		t.Errorf("report says %d -> %d, want 3 -> %d", rep.From, rep.To, core.SchemaVersion)
	}
	if len(rep.Stores) != 1 || rep.Stores[0].Tasks != 1 {
		t.Errorf("stores = %+v, want the one hot store with 1 shard", rep.Stores)
	}

	after, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("meta.json = %q, want it untouched (%q)", after, before)
	}
}

// --yes performs the flag day: meta.json carries the new layout (through the
// single marshaller path) and ordinary writes are unblocked again.
func TestUpgradeApplies(t *testing.T) {
	a, meta := outdatedBoard(t)

	if _, err := a.Add("blocked", AddOpts{}); err == nil {
		t.Fatal("precondition: an outdated board must refuse writes")
	}

	rep, err := a.Upgrade(true)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Changed || !rep.Applied {
		t.Errorf("apply = %+v, want changed:true applied:true", rep)
	}

	want, err := core.MarshalMeta(&core.Meta{SchemaVersion: core.SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("meta.json = %q, want the single-marshaller bytes %q", got, want)
	}
	if _, err := a.Add("now allowed", AddOpts{}); err != nil {
		t.Errorf("after the upgrade, writes must work: %v", err)
	}
}

// A board is often TWO stores on disk. Raising only the hot one would leave
// .furrow/archive/ behind, and the next `furrow archive` would die on its write
// gate — a store nobody remembers exists.
func TestUpgradeAlsoRaisesArchiveStore(t *testing.T) {
	dir := t.TempDir()
	a, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := a.Add("done thing", AddOpts{Status: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ArchiveIDs([]string{tk.ID}, false); err != nil {
		t.Fatal(err)
	}
	arcMeta := filepath.Join(dir, DirName, "archive", "meta.json")
	if _, err := os.Stat(arcMeta); err != nil {
		t.Fatalf("precondition: archive store must exist: %v", err)
	}

	old := []byte("{\n  \"schema_version\": 3\n}\n")
	for _, p := range []string{filepath.Join(dir, DirName, "meta.json"), arcMeta} {
		if err := os.WriteFile(p, old, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := a.Upgrade(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Stores) != 2 {
		t.Fatalf("stores = %+v, want both the hot store and archive/", rep.Stores)
	}
	got, err := os.ReadFile(arcMeta)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == string(old) {
		t.Error("archive/meta.json was left on the old layout — the next archive would refuse to write it")
	}
}

// `archive` is the one flow that writes TWO stores, and it commits the
// destination before destroying the source. On a read-only board that ordering
// leaked: the sibling .furrow/archive/ is born fresh, so it passed the store's
// fresh-stamp exemption and got written (stamped with the BINARY's layout, one
// ahead of the board that owns it) — and only THEN did the hot store's gate
// refuse. The task ended up in both stores, and a v4 archive sat under a v3
// board. A refusal has to be total, so the whole operation is gated up front.
func TestArchiveOnAnOutdatedBoardWritesNothing(t *testing.T) {
	dir := t.TempDir()
	a, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := a.Add("done thing", AddOpts{Status: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DirName, "meta.json"),
		[]byte("{\n  \"schema_version\": 3\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = a.ArchiveIDs([]string{tk.ID}, false)
	var fe *core.Error
	if !errors.As(err, &fe) || fe.ID != "schema-upgrade-required" {
		t.Fatalf("archive onto an outdated board = %v, want schema-upgrade-required", err)
	}

	// Nothing may have been born under the read-only board...
	if _, err := os.Stat(filepath.Join(dir, DirName, "archive")); !os.IsNotExist(err) {
		t.Error("a refused archive must not create the sibling archive store")
	}
	// ...and the task must still be exactly where it was, once.
	idx, err := a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, i := idx.Find(tk.ID); i < 0 {
		t.Error("the task left the hot store on a refused archive")
	}
}

// Safe to run at any time: a current board is a clean no-op, exit 0.
func TestUpgradeIdempotent(t *testing.T) {
	dir := t.TempDir()
	a, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := a.Upgrade(true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Changed || rep.Applied || len(rep.Stores) != 0 {
		t.Errorf("upgrade of a current board = %+v, want a no-op", rep)
	}
}

// A board NEWER than this binary is not something to "upgrade" — it means the
// BINARY is stale. Refuse (exit 3); there is no downgrade path.
func TestUpgradeRefusesNewerBoard(t *testing.T) {
	dir := t.TempDir()
	a, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DirName, "meta.json"),
		[]byte("{\n  \"schema_version\": 99\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = a.Upgrade(true)
	var fe *core.Error
	if !errors.As(err, &fe) || fe.ID != "schema-too-new" {
		t.Fatalf("Upgrade error = %v, want id schema-too-new", err)
	}
	if fe.Code != core.CodeInternal {
		t.Errorf("exit code = %d, want %d", fe.Code, core.CodeInternal)
	}
}

// `board` is the last thing that still answers when the board and the binary
// disagree — that is what makes it usable as CI's pre-flight. It must never fail.
func TestBoardReportsSchemaAndNeverFails(t *testing.T) {
	a, meta := outdatedBoard(t)
	b := a.Board()
	if b.SchemaVersion != 3 || b.BinarySchemaVersion != core.SchemaVersion {
		t.Errorf("board = v%d/v%d, want v3/v%d", b.SchemaVersion, b.BinarySchemaVersion, core.SchemaVersion)
	}
	if b.SchemaState != SchemaOutdated || b.Writable {
		t.Errorf("state = %q writable=%t, want %q/false", b.SchemaState, b.Writable, SchemaOutdated)
	}

	if err := os.WriteFile(meta, []byte("{\n  \"schema_version\": 99\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b := a.Board(); b.SchemaState != SchemaTooNew || b.Writable {
		t.Errorf("too-new board state = %q writable=%t, want %q/false", b.SchemaState, b.Writable, SchemaTooNew)
	}

	if err := os.WriteFile(meta, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b := a.Board(); b.SchemaState != SchemaUnreadable || b.Writable {
		t.Errorf("garbled board state = %q writable=%t, want %q/false", b.SchemaState, b.Writable, SchemaUnreadable)
	}
}

// An unstamped but EMPTY board (a bare `.furrow/`, or an `init` interrupted
// after config.toml but before the first Save) is version 0 — yet it is
// perfectly writable: the first write stamps it. Deriving `writable` from the two
// version integers called that board outdated and unwritable, which would have
// failed the CI pre-flight on a board that would have taken the write. Both
// `board` and `lint` must ask the store, which owns the rule.
func TestFreshUnstampedBoardIsWritable(t *testing.T) {
	dir := t.TempDir()
	a, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, DirName, "meta.json")); err != nil {
		t.Fatal(err)
	}

	b := a.Board()
	if !b.Writable || b.SchemaState != SchemaCurrent {
		t.Errorf("fresh unstamped board = {state:%q writable:%t}, want {%q true} — the first write stamps it",
			b.SchemaState, b.Writable, SchemaCurrent)
	}
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Code == "schema-outdated" {
			t.Errorf("a writable board must not warn schema-outdated: %+v", p)
		}
	}

	// And the write really does succeed, stamping the board on the way.
	if _, err := a.Add("first", AddOpts{}); err != nil {
		t.Fatalf("a fresh board must accept its first write: %v", err)
	}
	if v, _ := a.Store.BoardVersion(); v != core.SchemaVersion {
		t.Errorf("board version after the first write = %d, want %d", v, core.SchemaVersion)
	}
}

// lint WARNS on an outdated board (not errors): a read-only board is the
// legitimate middle of a flag day and must not red every repo's board-lint CI.
func TestLintWarnsOnOutdatedBoard(t *testing.T) {
	a, _ := outdatedBoard(t)
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	var found *core.Problem
	for i := range ps {
		if ps[i].Code == "schema-outdated" {
			found = &ps[i]
		}
	}
	if found == nil {
		t.Fatalf("lint = %+v, want a schema-outdated problem", ps)
	}
	if found.Severity != core.SevWarn {
		t.Errorf("severity = %v, want SevWarn (a flag day must not red CI)", found.Severity)
	}

	if _, err := a.Upgrade(true); err != nil {
		t.Fatal(err)
	}
	ps, err = a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Code == "schema-outdated" {
			t.Errorf("a current board must not warn: %+v", p)
		}
	}
}
