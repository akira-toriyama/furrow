package core

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sampleRepoRecord is one repo review record covering both timestamp states: a
// set human clock (the non-null pointer path) and a null agent clock, plus a
// sub-second time that must normalize to whole seconds.
func sampleRepoRecord() *RepoRecord {
	human := time.Date(2026, 6, 20, 1, 2, 3, 456_000_000, time.UTC) // sub-second: must truncate
	return &RepoRecord{
		Repo:              "akira-toriyama/furrow",
		LastReviewed:      &human,
		LastAgentReviewed: nil,
	}
}

func TestRepoRecordPath(t *testing.T) {
	if got := RepoRecordPath("akira-toriyama/furrow"); got != "repos/akira-toriyama__furrow.json" {
		t.Errorf("RepoRecordPath = %q, want %q", got, "repos/akira-toriyama__furrow.json")
	}
	if got := RepoStem("owner/my_repo"); got != "owner__my_repo" {
		t.Errorf("RepoStem = %q, want %q", got, "owner__my_repo")
	}
}

func TestMarshalRepoGolden(t *testing.T) {
	got, err := MarshalRepo(sampleRepoRecord())
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "repo.golden.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update first): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("MarshalRepo output != golden\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestMarshalRepoDeterministic: re-marshalling a record parsed from canonical
// bytes yields byte-identical output (zero churn when a repo shard is re-saved
// untouched) — the per-repo twin of the task determinism contract.
func TestMarshalRepoDeterministic(t *testing.T) {
	first, err := MarshalRepo(sampleRepoRecord())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := UnmarshalRepo(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalRepo(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("re-marshal not byte-identical\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestMarshalRepoNormalizesTimestamps: a sub-second, non-UTC clock is coerced to
// whole-second UTC, and a nil clock stays null.
func TestMarshalRepoNormalizesTimestamps(t *testing.T) {
	b, err := MarshalRepo(sampleRepoRecord())
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !bytes.Contains(b, []byte(`"last_reviewed": "2026-06-20T01:02:03Z"`)) {
		t.Errorf("last_reviewed not truncated to whole seconds:\n%s", got)
	}
	if !bytes.Contains(b, []byte(`"last_agent_reviewed": null`)) {
		t.Errorf("nil last_agent_reviewed should serialize as null:\n%s", got)
	}
}

// TestUnmarshalRepoRejectsGarbage: a malformed repo shard is a validation error.
func TestUnmarshalRepoRejectsGarbage(t *testing.T) {
	if _, err := UnmarshalRepo([]byte("{ not json")); err == nil {
		t.Error("expected a validation error on malformed repo shard")
	}
}
