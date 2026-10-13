package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// The small --batch defects the fourth drill hit (t-xm83): a line's own
// duplicate checklist rows and their ticks survive (the round trip `show
// --json` → `add --batch` promises), an over-long line is the operator's
// input (exit 2 naming the line and --body-file), a checklist key is matched
// by its exact spelling, a byte-order mark on line 1 is not a field, and a
// padded `--body " notes.md "` still names the file (a directory stays
// literal, as TestBodyThatIsNotAFileStaysLiteral pins).
func TestAddBatchBundle(t *testing.T) {
	initStore(t)

	stdout, _, code := runSplitIn(t, `{"title":"c","checklist":[{"text":"a","done":false},{"text":"a","done":true},"a"]}`+"\n", "--json", "add", "--batch", "-")
	if code != 0 {
		t.Fatalf("batch exit %d: %s", code, stdout)
	}
	var created []struct {
		Checklist []core.ChecklistItem `json:"checklist"`
	}
	if err := json.Unmarshal([]byte(stdout), &created); err != nil || len(created) != 1 {
		t.Fatalf("parse: %v %s", err, stdout)
	}
	if got := created[0].Checklist; len(got) != 3 || !got[1].Done {
		t.Errorf("a line's own checklist rows must survive as written, got %+v", got)
	}

	fe, _, _ := execCLI(t, strings.Repeat("x", 5*1024*1024)+"\n", "add", "--batch", "-")
	if fe == nil || fe.Kind != core.KindValidation || !strings.Contains(fe.Msg, "--body-file") {
		t.Errorf("an over-long line must be validation naming --body-file, got %+v", fe)
	}

	fe, _, _ = execCLI(t, `{"title":"c","checklist":[{"TEXT":"a","done":true}]}`+"\n", "add", "--batch", "-")
	if fe == nil || fe.Kind != core.KindValidation || !strings.Contains(fe.Msg, "TEXT") {
		t.Errorf("a mis-spelled checklist key must be refused by its exact spelling, got %+v", fe)
	}

	if _, _, code := runSplitIn(t, ""+`{"title":"bom"}`+"\n", "add", "--batch", "-"); code != 0 {
		t.Errorf("a byte-order mark on line 1 must not be read as a field (exit %d)", code)
	}

	dir := t.TempDir()
	notes := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(notes, []byte("# notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fe, _, _ = execCLI(t, "", "add", "padded", "--body", " "+notes+" ")
	if fe == nil || fe.Kind != core.KindValidation {
		t.Errorf("a padded path must still be refused as a body, got %+v", fe)
	}
}
