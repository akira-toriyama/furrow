package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// `config set` is strict where the reader is lenient: a value the reader would
// clamp away is refused BEFORE the write. default_repo is clamped by the APP
// layer (its shape check lives beside core.IsRepoShaped), and the regression
// guard compared only the config layer's warnings, so `config set default_repo
// notarepo` was exit 0, wrote the value, and the next read threw it away
// (t-ge22).
func TestConfigSetBoardRefusesADefaultRepoTheReaderWouldClamp(t *testing.T) {
	a := newFSApp(t)
	path := filepath.Join(a.Dir, "config.toml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"notarepo", "auto"} {
		_, err := a.ConfigSetBoard("default_repo", bad)
		fe := core.AsError(err)
		if fe == nil || fe.Code != core.CodeValidation {
			t.Fatalf("config set default_repo %q = %v, want exit 2", bad, err)
		}
		if !strings.Contains(fe.Msg, "default_repo") {
			t.Errorf("the refusal must name the clamp: %q", fe.Msg)
		}
		after, _ := os.ReadFile(path)
		if string(after) != string(before) {
			t.Fatalf("a refused set wrote the file")
		}
	}
	ed, err := a.ConfigSetBoard("default_repo", "o/r")
	if err != nil || len(ed.Changed) != 1 {
		t.Fatalf("a usable default_repo must be written: %v %+v", err, ed)
	}
	if strings.Contains(string(before), "\ndefault_repo = ") {
		t.Fatal("fixture: the template already carried default_repo")
	}
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), "default_repo = \"o/r\"") {
		t.Errorf("default_repo not written: %s", after)
	}
	if entries, _ := os.ReadDir(a.Dir); len(entries) > 0 {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".tmp-") {
				t.Errorf("atomic write left a temp behind: %s", e.Name())
			}
		}
	}
}
