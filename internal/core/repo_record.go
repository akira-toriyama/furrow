package core

import (
	"strings"
	"time"
)

// RepoRecord is one per-repo review record: the object in a single
// .furrow/repos/<owner>__<repo>.json shard. Like a task shard it is one entity
// per file (concurrent reviews of different repos touch disjoint files, never a
// shared map that two operators would rewrite at once), self-describing (Repo
// holds the canonical owner/repo, so the filename encoding never has to be
// decoded back), and carries NO schema_version — that lives in meta.json.
//
// Field order == JSON key order (see Task). The two timestamps are pointers so
// an unset clock serializes to explicit null (the Closed pattern), and they are
// deliberately SEPARATE: LastReviewed is the human review clock the staleness
// nudge reads, while LastAgentReviewed records an autonomous agent sweep WITHOUT
// advancing that clock — so "Claude re-evaluated this repo" is logged but never
// lets furrow stop nudging a human to look (the actor-separation the review
// design turns on).
type RepoRecord struct {
	Repo              string     `json:"repo"`                // "owner/repo"
	LastReviewed      *time.Time `json:"last_reviewed"`       // last human review; nil (-> null) = never
	LastAgentReviewed *time.Time `json:"last_agent_reviewed"` // last agent sweep; nil (-> null) = never

	// extras holds keys this binary does not know — a field written by a NEWER
	// furrow that did not bump SchemaVersion, so no version gate fired. Without it,
	// one ordinary write would silently destroy that field (see passthrough.go).
	// nil when the shard had no unknown keys, which is the normal case.
	//
	// UNEXPORTED, like Task's and Meta's: encoding/json cannot see it, so it never
	// becomes a key of its own, and no MarshalJSON re-emits it — the splice happens
	// on the store's write path (core.MarshalRepo -> encodeCanonicalWithExtras).
	// Read it back with ExtraKeys(); `furrow lint` warns unknown-shard-key on it.
	extras Extras
}

// RepoRecordPath returns the canonical relative shard path for a repo record:
// repos/<owner>__<repo>.json. The single "/" in an owner/repo becomes "__" so
// the identifier maps to one flat filename. The encoding is unambiguous because
// an owner never contains an underscore (see the repos pattern in schema/core),
// so the first "__" is always the owner/repo boundary — but nothing decodes the
// filename anyway; the record is self-describing via Repo. The store owns this
// path; callers never assemble it.
func RepoRecordPath(repo string) string { return "repos/" + RepoStem(repo) + ".json" }

// RepoStem maps an owner/repo identifier to its shard filename stem
// (owner/repo -> owner__repo). Exported so the store's list path can build the
// same names the marshaller expects.
func RepoStem(repo string) string { return strings.ReplaceAll(repo, "/", "__") }
