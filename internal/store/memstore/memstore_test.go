package memstore

import (
	"errors"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// memstore mirrors fsstore's version gate so app/cli tests can exercise both
// refusal directions without a disk.
func TestVersionGate(t *testing.T) {
	s := New("t-", 5)

	if _, err := s.Load(); err != nil {
		t.Fatalf("default store must load: %v", err)
	}

	if err := s.SetBoardVersion(core.SchemaVersion + 1); err != nil {
		t.Fatal(err)
	}
	_, err := s.Load()
	if err == nil {
		t.Fatal("Load of a newer board must fail")
	}
	if got := core.ExitCode(err); got != int(core.CodeInternal) {
		t.Errorf("Load exit code = %d, want %d", got, core.CodeInternal)
	}
	if !strings.Contains(err.Error(), "update furrow") {
		t.Errorf("error should say the fix: %q", err.Error())
	}
	if err := s.Save(&core.Index{}); err == nil {
		t.Fatal("Save onto a newer board must fail")
	}

	if err := s.SetBoardVersion(core.SchemaVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err != nil {
		t.Fatalf("restored version must load again: %v", err)
	}
}

// The twin of fsstore's TestSaveNeverRaisesBoardVersion: an OLDER board reads
// fine but refuses writes, and Save leaves its version exactly where it was.
func TestSaveNeverRaisesBoardVersion(t *testing.T) {
	s := New("t-", 5)
	old := core.SchemaVersion - 1
	if err := s.SetBoardVersion(old); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Load(); err != nil {
		t.Fatalf("an outdated board must still load: %v", err)
	}

	err := s.Save(&core.Index{Tasks: []core.Task{{ID: "t-0001", Title: "x"}}})
	var fe *core.Error
	if !errors.As(err, &fe) || fe.ID != "schema-upgrade-required" {
		t.Fatalf("Save error = %v, want id schema-upgrade-required", err)
	}
	if fe.Code != core.CodeValidation {
		t.Errorf("exit code = %d, want %d", fe.Code, core.CodeValidation)
	}
	if v, _ := s.BoardVersion(); v != old {
		t.Errorf("board version = %d, want it untouched at %d", v, old)
	}
}

func TestSaveAssetDedup(t *testing.T) {
	s := New("t-", 5)

	name, err := s.SaveAsset("t-0001", "shot.png", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	if name != "t-0001-shot.png" {
		t.Fatalf("first name = %q, want t-0001-shot.png", name)
	}
	name2, err := s.SaveAsset("t-0001", "shot.png", []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	if name2 != "t-0001-shot-2.png" {
		t.Fatalf("second name = %q, want t-0001-shot-2.png", name2)
	}
}

func TestListAssets(t *testing.T) {
	s := New("t-", 5)

	if got, err := s.ListAssets(); err != nil || got != nil {
		t.Fatalf("empty store: got %+v err %v, want nil,nil", got, err)
	}

	if _, err := s.SaveAsset("t-0001", "b.png", []byte("bb")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveAsset("t-0001", "a.png", []byte("aaaa")); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListAssets()
	if err != nil {
		t.Fatal(err)
	}
	// sorted by name, with byte sizes, so lint output is deterministic.
	want := []core.AssetInfo{
		{Name: "t-0001-a.png", Size: 4},
		{Name: "t-0001-b.png", Size: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("ListAssets = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ListAssets[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
