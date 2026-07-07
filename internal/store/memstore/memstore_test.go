package memstore

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// memstore mirrors fsstore's version gate so app/cli tests can exercise the
// "board newer than binary" path without a disk.
func TestVersionGate(t *testing.T) {
	s := New("t-", 5)

	if _, err := s.Load(); err != nil {
		t.Fatalf("default store must load: %v", err)
	}

	s.SetSchemaVersion(core.SchemaVersion + 1)
	_, err := s.Load()
	if err == nil {
		t.Fatal("Load of a newer board must fail")
	}
	if got := core.ExitCode(err); got != int(core.CodeInternal) {
		t.Errorf("Load exit code = %d, want %d", got, core.CodeInternal)
	}
	if !strings.Contains(err.Error(), "update the binary") {
		t.Errorf("error should say the fix: %q", err.Error())
	}
	if err := s.Save(&core.Index{}); err == nil {
		t.Fatal("Save onto a newer board must fail")
	}

	s.SetSchemaVersion(core.SchemaVersion)
	if _, err := s.Load(); err != nil {
		t.Fatalf("restored version must load again: %v", err)
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
