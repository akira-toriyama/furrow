package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v10/internal/app"
	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// An epic shard as a pre-v7 furrow wrote it — no `deps`, `standing` or
// `pinned`, the three keys `furrow schema epic` has since made required (the
// central board's e-0007, verbatim but for the title). Fixed bytes: the code
// under test did not write them.
const preV7EpicShard = `{
  "id": "e-0007",
  "title": "an old box",
  "goal": "",
  "active": false,
  "labels": [
    "web"
  ],
  "repos": [
    "akira-toriyama/vista"
  ],
  "meta": {},
  "created": "2026-06-25T02:57:43Z",
  "updated": "2026-07-16T08:36:29Z",
  "closed": "2026-07-16T08:36:29Z",
  "body": "bodies/e-0007.md"
}
`

// A repos/ shard from before `last_agent_reviewed` existed.
const preV9RepoShard = "{\n  \"repo\": \"o/old\",\n  \"last_reviewed\": \"2026-06-20T01:02:03Z\"\n}\n"

// requiredKeys is `furrow schema <kind>`'s top-level `required` list.
func requiredKeys(t *testing.T, kind string) []string {
	t.Helper()
	out, code := run(t, "schema", kind)
	if code != 0 {
		t.Fatalf("schema %s: exit %d:\n%s", kind, code, out)
	}
	var s struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal([]byte(out), &s); err != nil || len(s.Required) == 0 {
		t.Fatalf("schema %s: no required list (%v):\n%s", kind, err, out)
	}
	return s.Required
}

func missingKeys(t *testing.T, path string, required []string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var missing []string
	for _, k := range required {
		if _, ok := obj[k]; !ok {
			missing = append(missing, k)
		}
	}
	return missing
}

// `upgrade` re-serializes EVERY shard kind, in both stores: an epic or repo
// shard made before a key became required gains it, the preview counts what
// the apply rewrites, and a shard already in today's shape is left alone.
func TestUpgradeRewritesOldEpicAndRepoShards(t *testing.T) {
	initStore(t)
	root := os.Getenv(app.EnvDir)
	fresh := addEpic(t, "a box written today")
	done := addTask(t, "to be archived", "-s", "done")
	if out, code := run(t, "archive", done, "--yes"); code != 0 {
		t.Fatalf("archive: exit %d:\n%s", code, out)
	}

	hotEpic := filepath.Join(root, "epics", "e-0007.json")
	arcEpic := filepath.Join(root, "archive", "epics", "e-0007.json")
	repo := filepath.Join(root, "repos", core.RepoStem("o/old")+".json")
	freshEpic := filepath.Join(root, "epics", fresh+".json")
	for path, content := range map[string]string{hotEpic: preV7EpicShard, arcEpic: preV7EpicShard, repo: preV9RepoShard} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	freshBefore, err := os.ReadFile(freshEpic)
	if err != nil {
		t.Fatal(err)
	}

	epicRequired, repoRequired := requiredKeys(t, "epic"), requiredKeys(t, "repo")
	if len(missingKeys(t, hotEpic, epicRequired)) == 0 || len(missingKeys(t, repo, repoRequired)) == 0 {
		t.Fatal("precondition: the fixtures must lack a key the schema requires")
	}

	// One layout behind, in both stores: the smallest hop upgrade acts on.
	behind := []byte("{\n  \"schema_version\": " + strconv.Itoa(core.SchemaVersion-1) + "\n}\n")
	for _, meta := range []string{filepath.Join(root, "meta.json"), filepath.Join(root, "archive", "meta.json")} {
		if err := os.WriteFile(meta, behind, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	type report struct {
		Applied bool `json:"applied"`
		Stores  []struct {
			Path           string `json:"path"`
			EpicsRewritten *int   `json:"epics_rewritten"`
			ReposRewritten *int   `json:"repos_rewritten"`
		} `json:"stores"`
	}
	wantCounts := func(label string, rep report) {
		t.Helper()
		if len(rep.Stores) != 2 {
			t.Fatalf("%s: want the hot store and archive/, got %+v", label, rep.Stores)
		}
		for i, want := range [][2]int{{1, 1}, {1, 0}} {
			s := rep.Stores[i]
			if s.EpicsRewritten == nil || s.ReposRewritten == nil {
				t.Fatalf("%s: %s lacks epics_rewritten / repos_rewritten", label, s.Path)
			}
			if *s.EpicsRewritten != want[0] || *s.ReposRewritten != want[1] {
				t.Errorf("%s: %s rewrote %d epic(s), %d repo(s); want %d, %d",
					label, s.Path, *s.EpicsRewritten, *s.ReposRewritten, want[0], want[1])
			}
		}
	}

	// The preview counts what the apply will rewrite, and writes nothing.
	var preview report
	out, code := run(t, "--json", "upgrade")
	if code != 0 {
		t.Fatalf("upgrade preview: exit %d:\n%s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	wantCounts("preview", preview)
	if b, _ := os.ReadFile(hotEpic); string(b) != preV7EpicShard {
		t.Errorf("the preview rewrote the epic shard:\n%s", b)
	}

	var applied report
	out, code = run(t, "--json", "upgrade", "--yes")
	if code != 0 {
		t.Fatalf("upgrade --yes: exit %d:\n%s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &applied); err != nil {
		t.Fatal(err)
	}
	if !applied.Applied {
		t.Fatalf("upgrade --yes did not apply:\n%s", out)
	}
	wantCounts("apply", applied)

	for _, path := range []string{hotEpic, arcEpic, freshEpic} {
		if m := missingKeys(t, path, epicRequired); len(m) != 0 {
			t.Errorf("%s still lacks required %v after the upgrade", path, m)
		}
	}
	if m := missingKeys(t, repo, repoRequired); len(m) != 0 {
		t.Errorf("%s still lacks required %v after the upgrade", repo, m)
	}
	if b, _ := os.ReadFile(freshEpic); string(b) != string(freshBefore) {
		t.Errorf("a shard already in today's shape was rewritten:\n%s", b)
	}

	// What a reader gets: an array, where the old shard answered null.
	var shown struct {
		Deps json.RawMessage `json:"deps"`
	}
	out, code = run(t, "--json", "epic", "show", "e-0007", "--no-body")
	if code != 0 {
		t.Fatalf("epic show: exit %d:\n%s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatal(err)
	}
	if string(shown.Deps) != "[]" {
		t.Errorf("epic show: deps = %s, want []", shown.Deps)
	}
}

// A shard that does not parse stops the flag day before the first write, in
// whichever store it sits, and the refusal names the file from the board root.
func TestUpgradeRefusesATornShardAndNamesIt(t *testing.T) {
	for _, tc := range []struct{ file, want string }{
		{filepath.Join("epics", "e-torn.json"), "epics/e-torn.json: "},
		{filepath.Join("archive", "epics", "e-torn.json"), "archive/epics/e-torn.json: "},
		{filepath.Join("repos", "o__torn.json"), "repos/o__torn.json: "},
	} {
		initStore(t)
		root := os.Getenv(app.EnvDir)
		done := addTask(t, "to be archived", "-s", "done")
		mustRun(t, "archive", done, "--yes")
		torn := filepath.Join(root, tc.file)
		if err := os.MkdirAll(filepath.Dir(torn), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(torn, []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		behind := []byte("{\n  \"schema_version\": " + strconv.Itoa(core.SchemaVersion-1) + "\n}\n")
		metas := []string{filepath.Join(root, "meta.json"), filepath.Join(root, "archive", "meta.json")}
		for _, meta := range metas {
			if err := os.WriteFile(meta, behind, 0o644); err != nil {
				t.Fatal(err)
			}
		}

		fe, _ := runErr(t, "upgrade", "--yes")
		if fe == nil || fe.Kind != core.KindValidation {
			t.Fatalf("%s: err = %+v, want a validation refusal", tc.file, fe)
		}
		if !strings.HasPrefix(fe.Msg, tc.want) {
			t.Errorf("%s: message must lead with %q: %s", tc.file, tc.want, fe.Msg)
		}
		for _, meta := range metas {
			if b, _ := os.ReadFile(meta); string(b) != string(behind) {
				t.Errorf("%s: the refusal still raised %s:\n%s", tc.file, meta, b)
			}
		}
	}
}
