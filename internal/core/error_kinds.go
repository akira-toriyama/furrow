package core

import "sort"

// Error kinds — the closed vocabulary behind Error.Kind, the field consumers
// branch on (never the message prose, never the exit code alone). Every failure
// furrow returns carries exactly one of these kebab-case classes.
//
// The generic trio classifies the long tail by remedy, mirroring the exit-code
// contract (not-found = fix the reference; validation = fix the input, do not
// retry; internal = report / fix the environment). A named kind exists exactly
// where the remedy is MORE specific than the exit code says — retry, run
// `furrow upgrade`, update furrow, resolve a conflict, pick a candidate — or
// where docs/CI already branch on the class (the sync family).
//
// Discipline (same as lintCodes, plus the compiler):
//   - EVERY Kind* constant is registered in errorKinds below;
//     TestErrorKindListMatchesConstants pins the two together.
//   - Emission sites use the constants, never a string literal, so an
//     unregistered kind cannot compile; TestErrorKindEmissionUsesConstants
//     (internal/cli) greps the tree for stray literals.
//   - `furrow vocab error-kinds` prints this list — the docs drift guard
//     (scripts/check-docs-vocab.sh) reads it, so prose enumerations can be
//     claimed instead of hand-maintained.
const (
	KindNotFound   = "not-found"  // a specifically requested id/name does not exist (exit 1)
	KindValidation = "validation" // bad usage or invalid input — fix the args, do not retry (exit 2)
	KindInternal   = "internal"   // an unclassified internal / IO failure (exit 3)

	KindBodyConflictMarker    = "body-conflict-marker"    // a body still carries git conflict markers; refuse to commit it
	KindDoctorUnhealthy       = "doctor-unhealthy"        // `furrow doctor` found problems (exit 1, shared with not-found)
	KindEpicActiveClash       = "epic-active-clash"       // another epic already holds this repo's active slot
	KindEpicAmbiguous         = "epic-ambiguous"          // an epic ref matched several boxes; candidates carries them
	KindEpicNotFound          = "epic-not-found"          // an epic ref resolved to no box
	KindGitFailed             = "git-failed"              // a git invocation failed; the message names the command
	KindGitMissing            = "git-missing"             // git itself is not on PATH
	KindNoGitRepo             = "no-git-repo"             // the board dir is not inside a git repository
	KindQueryParse            = "query-parse"             // -q query failed to parse
	KindQueryType             = "query-type"              // -q term used a value form the field does not take
	KindQueryUnknownField     = "query-unknown-field"     // -q named an unknown qualifier; candidates carries the vocabulary
	KindQueryUnknownFlag      = "query-unknown-flag"      // -q is: named an unknown flag; candidates carries the vocabulary
	KindRepoAmbiguous         = "repo-ambiguous"          // a repo short name matched several repos; candidates carries them
	KindRepoUnknown           = "repo-unknown"            // a repo arg matched no known repo
	KindSchemaTooNew          = "schema-too-new"          // the BOARD is ahead of this binary — update furrow (exit 3)
	KindSchemaUpgradeRequired = "schema-upgrade-required" // the board is behind this binary — run `furrow upgrade` (exit 2)
	KindSyncBusy              = "sync-busy"               // a concurrent writer's rebase is still in flight — retryable
	KindSyncConflict          = "sync-conflict"           // the pull rebase hit real conflicts; details.paths names them
	KindSyncInterrupted       = "sync-interrupted"        // a signal cancelled the in-flight git — retryable
	KindSyncLockStale         = "sync-lock-stale"         // a git lock/fetch race outlived the retry budget; remove the stale .git/*.lock
	KindSyncOpInProgress      = "sync-op-in-progress"     // a non-rebase git operation (your own merge, say) blocks sync
	KindSyncPushRejected      = "sync-push-rejected"      // a co-writer kept winning the push race — retryable
	KindSyncStashStranded     = "sync-stash-stranded"     // the autostash could not re-apply; your edits sit in the stash
	KindSyncUnmerged          = "sync-unmerged"           // unmerged paths with no operation in progress block sync
	KindUnknownLane           = "unknown-lane"            // a lane token is outside the configured vocabulary; candidates carries it
	KindUnknownSubcommand     = "unknown-subcommand"      // an unknown subcommand; candidates carries the known ones
)

// errorKinds is the registry ErrorKindList/IsErrorKind read. Every Kind*
// constant above appears here exactly once (pinned by
// TestErrorKindListMatchesConstants).
var errorKinds = map[string]bool{
	KindNotFound:   true,
	KindValidation: true,
	KindInternal:   true,

	KindBodyConflictMarker:    true,
	KindDoctorUnhealthy:       true,
	KindEpicActiveClash:       true,
	KindEpicAmbiguous:         true,
	KindEpicNotFound:          true,
	KindGitFailed:             true,
	KindGitMissing:            true,
	KindNoGitRepo:             true,
	KindQueryParse:            true,
	KindQueryType:             true,
	KindQueryUnknownField:     true,
	KindQueryUnknownFlag:      true,
	KindRepoAmbiguous:         true,
	KindRepoUnknown:           true,
	KindSchemaTooNew:          true,
	KindSchemaUpgradeRequired: true,
	KindSyncBusy:              true,
	KindSyncConflict:          true,
	KindSyncInterrupted:       true,
	KindSyncLockStale:         true,
	KindSyncOpInProgress:      true,
	KindSyncPushRejected:      true,
	KindSyncStashStranded:     true,
	KindSyncUnmerged:          true,
	KindUnknownLane:           true,
	KindUnknownSubcommand:     true,
}

// IsErrorKind reports whether k is a registered error kind (see errorKinds).
func IsErrorKind(k string) bool { return errorKinds[k] }

// ErrorKindList returns every registered error kind, sorted — the machine
// source behind `furrow vocab error-kinds` and the docs drift guard.
func ErrorKindList() []string {
	out := make([]string, 0, len(errorKinds))
	for k := range errorKinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
