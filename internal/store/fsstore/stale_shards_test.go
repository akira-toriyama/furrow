package fsstore

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// An epic shard as a pre-v7 furrow wrote it: no `deps`, `standing` or `pinned`
// (the central board's e-0007, verbatim but for the title). Fixed bytes, not
// produced by the code under test.
const preV7Epic = `{
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

func writeShard(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// StaleEpicShards names exactly the shards a re-save would change: not the one
// the marshaller wrote, not one filed under another id's name (re-saving it
// would write a second file), and nothing on a store with no epics/.
func TestStaleEpicShards(t *testing.T) {
	s, root := epicStore(t)
	if got, err := s.StaleEpicShards(); err != nil || got != nil {
		t.Fatalf("no epics/: got %v, %v; want nil, nil", got, err)
	}
	if err := s.Save(&core.Index{Tasks: []core.Task{}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEpic(epicNamed("e-fresh", "written today")); err != nil {
		t.Fatal(err)
	}
	writeShard(t, filepath.Join(root, "epics", "e-0007.json"), preV7Epic)
	writeShard(t, filepath.Join(root, "epics", "e-misfiled.json"), preV7Epic)

	got, err := s.StaleEpicShards()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"e-0007"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stale = %v, want %v", got, want)
	}

	// Listing is a read: the old shard is still the old bytes.
	if b, _ := os.ReadFile(filepath.Join(root, "epics", "e-0007.json")); string(b) != preV7Epic {
		t.Errorf("StaleEpicShards wrote the shard:\n%s", b)
	}

	// A shard that does not parse fails the call, as it fails LoadEpics.
	writeShard(t, filepath.Join(root, "epics", "e-torn.json"), "{")
	if _, err := s.StaleEpicShards(); err == nil {
		t.Error("an unparsable epic shard must fail the listing")
	}
}

// The repos/ twin: a record written before `last_agent_reviewed` existed is
// stale and named by owner/repo; one the marshaller wrote is not.
func TestStaleRepoShards(t *testing.T) {
	s, root := epicStore(t)
	if got, err := s.StaleRepoShards(); err != nil || got != nil {
		t.Fatalf("no repos/: got %v, %v; want nil, nil", got, err)
	}
	if err := s.Save(&core.Index{Tasks: []core.Task{}}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 6, 20, 1, 2, 3, 0, time.UTC)
	if err := s.SaveRepo(&core.RepoRecord{Repo: "o/fresh", LastReviewed: &at}); err != nil {
		t.Fatal(err)
	}
	old := "{\n  \"repo\": \"o/old\",\n  \"last_reviewed\": \"2026-06-20T01:02:03Z\"\n}\n"
	writeShard(t, filepath.Join(root, "repos", core.RepoStem("o/old")+".json"), old)
	writeShard(t, filepath.Join(root, "repos", core.RepoStem("o/elsewhere")+".json"), old)

	got, err := s.StaleRepoShards()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"o/old"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stale = %v, want %v", got, want)
	}
}
