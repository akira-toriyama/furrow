package fsstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

var gateLanes = []string{"inbox", "ready", "done"}

// A board whose meta.json declares a NEWER layout than this binary must refuse
// to Load and to Save (exit 3): a lenient unmarshal would silently drop fields
// the binary doesn't know, and a Save would write that loss back to disk.
func TestVersionGateRefusesNewerBoard(t *testing.T) {
	root := t.TempDir()
	s := New(root, gateLanes, "t-", 5)

	// A normal Save stamps the current version — loads fine.
	if err := s.Save(&core.Index{SchemaVersion: core.SchemaVersion, Tasks: []core.Task{
		{ID: "t-0001", Title: "x", Status: "ready", Priority: 100, Body: core.BodyPath("t-0001")},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err != nil {
		t.Fatalf("current-version board must load: %v", err)
	}

	// Hand-bump meta.json to a future version: both directions gate.
	meta := filepath.Join(root, "meta.json")
	if err := os.WriteFile(meta, []byte("{\n  \"schema_version\": 99\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := s.Load()
	if err == nil {
		t.Fatal("Load of a v99 board must fail")
	}
	if got := core.ExitCode(err); got != int(core.CodeInternal) {
		t.Errorf("Load exit code = %d, want %d", got, core.CodeInternal)
	}
	if !strings.Contains(err.Error(), "update the binary") {
		t.Errorf("Load error should say the fix: %q", err.Error())
	}

	if err := s.Save(&core.Index{SchemaVersion: core.SchemaVersion}); err == nil {
		t.Fatal("Save onto a v99 board must fail")
	} else if got := core.ExitCode(err); got != int(core.CodeInternal) {
		t.Errorf("Save exit code = %d, want %d", got, core.CodeInternal)
	}

	// The refused Save must not have touched the board: the shard is intact.
	if b, err := os.ReadFile(filepath.Join(root, "tasks", "t-0001.json")); err != nil || len(b) == 0 {
		t.Errorf("refused Save must leave shards intact: %v", err)
	}
}

// An OLDER (or absent) meta version still loads — forward compatibility is the
// store's normal lenient read; only the future direction is fatal.
func TestVersionGateAllowsOlderMeta(t *testing.T) {
	root := t.TempDir()
	s := New(root, gateLanes, "t-", 5)
	if err := s.Save(&core.Index{SchemaVersion: core.SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "meta.json"), []byte("{\n  \"schema_version\": 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := s.Load()
	if err != nil {
		t.Fatalf("older board must load: %v", err)
	}
	if idx.SchemaVersion != 1 {
		t.Errorf("loaded SchemaVersion = %d, want the board's 1", idx.SchemaVersion)
	}
}

// THE regression test for the 2026-07-13 outage: a routine write from a binary
// that knows a newer layout silently rewrote the SHARED board's meta.json from
// v3 to v4, and every release pinned by the fleet's CI lost the board at once.
// An ordinary Save must NEVER raise the board's declared version — it refuses,
// and `furrow upgrade` is the only thing that may raise it. Do not delete this.
func TestSaveNeverRaisesBoardVersion(t *testing.T) {
	root := t.TempDir()
	s := New(root, gateLanes, "t-", 5)
	if err := s.Save(&core.Index{Tasks: []core.Task{
		{ID: "t-0001", Title: "x", Status: "ready", Priority: 100, Body: core.BodyPath("t-0001")},
	}}); err != nil {
		t.Fatal(err)
	}

	// Put the board one layout behind this binary — a board written by the last
	// release, while the operator runs a source build of HEAD.
	metaPath := filepath.Join(root, "meta.json")
	old := []byte("{\n  \"schema_version\": " + strconv.Itoa(core.SchemaVersion-1) + "\n}\n")
	if err := os.WriteFile(metaPath, old, 0o644); err != nil {
		t.Fatal(err)
	}

	// Reads still work: an older board is readable, just not writable.
	if _, err := s.Load(); err != nil {
		t.Fatalf("an outdated board must still load: %v", err)
	}

	err := s.Save(&core.Index{Tasks: []core.Task{
		{ID: "t-0001", Title: "x", Status: "ready", Priority: 100, Body: core.BodyPath("t-0001")},
		{ID: "t-0002", Title: "new", Status: "ready", Priority: 110, Body: core.BodyPath("t-0002")},
	}})
	if err == nil {
		t.Fatal("Save onto an outdated board must refuse — it used to silently migrate it")
	}
	var fe *core.Error
	if !errors.As(err, &fe) || fe.ID != "schema-upgrade-required" {
		t.Fatalf("Save error = %v, want id schema-upgrade-required", err)
	}
	if fe.Code != core.CodeValidation {
		t.Errorf("Save exit code = %d, want %d (an explicit command fixes it)", fe.Code, core.CodeValidation)
	}

	// The refusal must be total: meta.json byte-identical, no new shard.
	if got, err := os.ReadFile(metaPath); err != nil || !bytes.Equal(got, old) {
		t.Errorf("meta.json = %q, want it untouched (%q); err=%v", got, old, err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "t-0002.json")); !os.IsNotExist(err) {
		t.Error("a refused Save must write no shard")
	}
}

// A populated board with NO meta.json is the same lie by another door: stamping
// it with this binary's version would claim a layout the shards were never
// written in. Reads still degrade leniently; writes refuse.
func TestSaveRefusesShardsWithoutMeta(t *testing.T) {
	root := t.TempDir()
	s := New(root, gateLanes, "t-", 5)
	if err := s.Save(&core.Index{Tasks: []core.Task{
		{ID: "t-0001", Title: "x", Status: "ready", Priority: 100, Body: core.BodyPath("t-0001")},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "meta.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err != nil {
		t.Fatalf("a board with shards but no meta must still load: %v", err)
	}
	err := s.Save(&core.Index{Tasks: []core.Task{
		{ID: "t-0001", Title: "x", Status: "ready", Priority: 100, Body: core.BodyPath("t-0001")},
	}})
	var fe *core.Error
	if !errors.As(err, &fe) || fe.ID != "schema-upgrade-required" {
		t.Fatalf("Save error = %v, want id schema-upgrade-required", err)
	}
	if _, err := os.Stat(filepath.Join(root, "meta.json")); !os.IsNotExist(err) {
		t.Error("the refused Save must not have re-stamped meta.json")
	}
}

// An outdated board is READ-ONLY — and that means every write, not just the
// shard write. The subtle one is SaveRepo: repos/<owner>__<repo>.json is a
// schema-v4-only artifact (`furrow review`), so writing one onto a v3 board
// plants a field the board never promised — the same lie the shard gate exists
// to prevent, through a door that doesn't go through Save(). SaveBody/SaveAsset
// matter for a different reason: without the gate, a refused `add` still leaves
// its body on disk (an orphan the next lint flags), so the refusal isn't total.
func TestOutdatedBoardIsReadOnlyForEveryWrite(t *testing.T) {
	root := t.TempDir()
	s := New(root, gateLanes, "t-", 5)
	if err := s.Save(&core.Index{Tasks: []core.Task{}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "meta.json"),
		[]byte("{\n  \"schema_version\": "+strconv.Itoa(core.SchemaVersion-1)+"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writes := map[string]func() error{
		"Save":     func() error { return s.Save(&core.Index{Tasks: []core.Task{{ID: "t-0001", Title: "x"}}}) },
		"SaveBody": func() error { return s.SaveBody("t-0001", "# x\n") },
		"SaveRepo": func() error { return s.SaveRepo(&core.RepoRecord{Repo: "owner/app"}) },
		"SaveAsset": func() error {
			_, err := s.SaveAsset("t-0001", "shot.png", []byte("x"))
			return err
		},
		"DeleteBody":  func() error { return s.DeleteBody("t-0001") },
		"DeleteAsset": func() error { return s.DeleteAsset("t-0001-shot.png") },
	}
	for name, w := range writes {
		t.Run(name, func(t *testing.T) {
			var fe *core.Error
			if err := w(); !errors.As(err, &fe) || fe.ID != "schema-upgrade-required" {
				t.Fatalf("%s on an outdated board = %v, want schema-upgrade-required", name, err)
			}
		})
	}

	// Nothing may have reached the disk.
	for _, p := range []string{"tasks/t-0001.json", "bodies/t-0001.md", "repos/owner__app.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); !os.IsNotExist(err) {
			t.Errorf("%s exists — a read-only board must refuse every write, totally", p)
		}
	}

	// Reads keep working throughout — that is the other half of the contract.
	if _, err := s.Load(); err != nil {
		t.Errorf("reads must survive an outdated board: %v", err)
	}
	if _, _, err := s.LoadRepo("owner/app"); err != nil {
		t.Errorf("repo reads must survive an outdated board: %v", err)
	}
}

// A fresh store (no meta.json, no shards) is the ONE case Save may stamp: there
// is no prior layout to lie about. This is what keeps `furrow init` working.
func TestSaveStampsAFreshStore(t *testing.T) {
	root := t.TempDir()
	s := New(root, gateLanes, "t-", 5)
	if err := s.Save(&core.Index{Tasks: []core.Task{}}); err != nil {
		t.Fatal(err)
	}
	v, err := s.BoardVersion()
	if err != nil {
		t.Fatal(err)
	}
	if v != core.SchemaVersion {
		t.Errorf("fresh board version = %d, want %d", v, core.SchemaVersion)
	}
}

// A garbled meta.json used to fall back to "whatever this binary is", which
// silently DISABLED the version gate. It is an error now: the board's declared
// layout is either readable or the operator restores it.
func TestMetaGarbledIsAnError(t *testing.T) {
	root := t.TempDir()
	s := New(root, gateLanes, "t-", 5)
	if err := s.Save(&core.Index{Tasks: []core.Task{}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "meta.json"), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("a garbled meta.json must not silently self-heal into the current version")
	} else if got := core.ExitCode(err); got != int(core.CodeInternal) {
		t.Errorf("exit code = %d, want %d", got, core.CodeInternal)
	}
	if err := s.Save(&core.Index{Tasks: []core.Task{}}); err == nil {
		t.Fatal("Save onto a garbled meta.json must fail")
	}
}

// SetBoardVersion is the one deliberate raiser (`furrow upgrade`'s engine): it
// unblocks writing, and it routes through the single marshaller path.
func TestSetBoardVersionUnblocksWrites(t *testing.T) {
	root := t.TempDir()
	s := New(root, gateLanes, "t-", 5)
	if err := s.Save(&core.Index{Tasks: []core.Task{}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "meta.json"),
		[]byte("{\n  \"schema_version\": "+strconv.Itoa(core.SchemaVersion-1)+"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(&core.Index{Tasks: []core.Task{}}); err == nil {
		t.Fatal("precondition: an outdated board refuses writes")
	}
	if err := s.SetBoardVersion(core.SchemaVersion); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(&core.Index{Tasks: []core.Task{
		{ID: "t-0001", Title: "x", Status: "ready", Priority: 100, Body: core.BodyPath("t-0001")},
	}}); err != nil {
		t.Fatalf("after the deliberate raise, writes must work: %v", err)
	}
	want, err := core.MarshalMeta(&core.Meta{SchemaVersion: core.SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("meta.json = %q, want the single-marshaller bytes %q", got, want)
	}
}

// SetBoardVersion is the one writer of meta.json, and it must RAISE the version
// without eating the rest of the file. Building a fresh core.Meta{SchemaVersion:v}
// discards every key the file had — so `furrow upgrade`, the command whose entire
// job is to move a board FORWARD, would destroy the forward-compatible keys a
// newer furrow had written there. The passthrough is worthless if the upgrade
// path is the thing that strips.
func TestSetBoardVersionPreservesUnknownMetaKeys(t *testing.T) {
	root := t.TempDir()
	s := New(root, gateLanes, "t-", 5)
	if err := s.Save(&core.Index{Tasks: []core.Task{}}); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(root, "meta.json")
	if err := os.WriteFile(meta, []byte("{\n  \"schema_version\": "+strconv.Itoa(core.SchemaVersion-1)+",\n  \"min_reader\": 3\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.SetBoardVersion(core.SchemaVersion); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("min_reader")) {
		t.Errorf("raising the board ate a key it did not understand:\n%s", got)
	}
	v, err := s.BoardVersion()
	if err != nil {
		t.Fatal(err)
	}
	if v != core.SchemaVersion {
		t.Errorf("board version = %d, want %d — the raise itself must still happen", v, core.SchemaVersion)
	}
}
