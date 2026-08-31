package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// set --add-repo/--rm-repo: the repos-field mirror of the label pair (t-41hg —
// labels and epic could bulk-edit through set while repos alone needed N
// `repo <id>` calls). Bulk attach in one write, Rerepo's strict resolution,
// and removing the last repo leaves a first-class draft.
func TestSetAddRmRepos(t *testing.T) {
	initStore(t)
	t1 := addTitled(t, "one", "-r", "owner/app")
	t2 := addTitled(t, "two")

	// Bulk attach across several ids — one all-or-nothing write.
	if out, code := run(t, "set", t1, t2, "--add-repo", "owner/lib"); code != 0 {
		t.Fatalf("bulk add-repo: exit %d\n%s", code, out)
	}
	for _, id := range []string{t1, t2} {
		if out, _ := run(t, "show", id, "--json"); !strings.Contains(out, "owner/lib") {
			t.Errorf("%s missing the attached repo: %s", id, out)
		}
	}

	// Short-name detach resolves against the board's repo universe.
	if out, code := run(t, "set", t1, "--rm-repo", "app"); code != 0 {
		t.Fatalf("short-name rm: exit %d\n%s", code, out)
	}
	if out, _ := run(t, "show", t1, "--json"); strings.Contains(out, "owner/app") {
		t.Errorf("short-name --rm-repo did not detach owner/app: %s", out)
	}

	if _, code := run(t, "set", t1, "--rm-repo", "owner/lib"); code != 0 {
		t.Fatal("removing the last repo must succeed")
	}
	if out, _ := run(t, "ls", "--drafts"); !strings.Contains(out, t1) {
		t.Errorf("a task stripped of every repo must list as a draft:\n%s", out)
	}
}

// The guards: a blank value is exit 2 (never a silent no-op), and a short name
// resolving to nothing is exit 2 (never a silent new repo).
func TestSetRepoGuards(t *testing.T) {
	initStore(t)
	id := addTitled(t, "guarded", "-r", "owner/app")

	if fe, _ := runErr(t, "set", id, "--add-repo", ""); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("blank --add-repo = %+v, want exit 2", fe)
	}
	if fe, _ := runErr(t, "set", id, "--add-repo", "nosuchshortname"); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("unresolvable short name = %+v, want exit 2", fe)
	}
}
