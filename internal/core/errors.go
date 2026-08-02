package core

import (
	"errors"
	"fmt"
)

// Code is furrow's process exit-code contract. The CLI
// maps a returned error to one of these on the way out. Keep the meanings
// stable — scripts and Claude Code branch on them.
type Code int

const (
	CodeOK         Code = 0 // success (including an empty query result — a match of nothing still succeeded)
	CodeNotFound   Code = 1 // a specifically requested id was not found (e.g. `show <id>`); NOT an empty list
	CodeValidation Code = 2 // bad usage or invalid input — fix the args, do not retry
	CodeInternal   Code = 3 // internal / IO failure

	// CodeUnhealthy is `furrow doctor`'s "problems found" (the health-check
	// convention: brew doctor, git fsck). It deliberately shares exit 1 with
	// CodeNotFound — both mean "the thing you asked about is not in the state
	// you asked for", never bad usage (2) and never an internal failure (3) —
	// while the error id ("doctor-unhealthy") keeps the two distinguishable.
	CodeUnhealthy Code = 1
)

// Error is furrow's structured error. On a non-zero exit the CLI prints it to
// stderr as {"error":{"kind","subject","retryable","exit","message"
// [,"details"][,"candidates"]}} so callers get a machine-readable failure.
// Plain (non-*Error) errors are treated as CodeInternal.
type Error struct {
	// Code is the process exit code (rendered as "exit" in the envelope — the
	// word "code" is lint's, where it means the kebab-case problem slug).
	Code Code
	// Kind is the stable kebab-case failure class — THE field consumers branch
	// on. Closed vocabulary: every value is a Kind* constant registered in
	// ErrorKindList (error_kinds.go); `furrow vocab error-kinds` prints it.
	Kind string
	// Subject names the entity the failure is about — a task or epic id, an
	// owner/repo, an asset name, or a store file ("config", "meta", "index");
	// "" when the failure has no single subject. Kind and Subject used to
	// share one overloaded ID field; they are separate so a consumer never
	// has to guess whether it is reading a slug or a task id.
	Subject string
	Msg     string
	// Details is optional machine-actionable payload for errors where the
	// message alone isn't enough to act on (e.g. sync-conflict carries
	// {"paths": [...]}). Rendered as "details" in the envelope when non-nil;
	// consumers that only read kind/message are unaffected.
	Details any
	// Candidates is the optional list of concrete alternatives when an input
	// almost resolved — an ambiguous repo short name, or the did-you-mean
	// guard's repo suggestion. Rendered as "candidates" in the envelope when
	// non-empty, so agents branch on the array and never regex the prose.
	Candidates []string
	// Retryable marks transient failures whose documented recovery is
	// re-running the same command (sync-busy, sync-push-rejected,
	// sync-interrupted). Agents branch on this ONE key — retry on true,
	// escalate or fix the input on false — instead of memorizing which kinds
	// are transient. Always rendered in the envelope, even when false.
	Retryable bool
}

func (e *Error) Error() string { return e.Msg }

// WithPrefixf returns a COPY of e whose message is prefixed with the formatted
// context, keeping the code, kind, subject, details, and — the point of it —
// Candidates.
// A bulk path (AddMany) must say WHICH input failed without degrading the error
// shape the single-item path returns: rebuilding the message with Validationf
// silently drops the did-you-mean vocabulary an agent branches on. e is not
// mutated, so a shared error value stays safe to prefix per item.
func (e *Error) WithPrefixf(format string, a ...any) *Error {
	c := *e
	c.Msg = fmt.Sprintf(format, a...) + e.Msg
	return &c
}

// NotFound builds a CodeNotFound / KindNotFound error for a missing task id.
func NotFound(id string) *Error {
	return &Error{Code: CodeNotFound, Kind: KindNotFound, Subject: id, Msg: fmt.Sprintf("task not found: %s", id)}
}

// Validationf builds a CodeValidation error (bad input) under the generic
// KindValidation. subject names the offending entity ("" when there is none);
// a site whose failure class is more specific sets Kind on the returned error
// (or builds the Error literally with a named Kind* constant).
func Validationf(subject, format string, a ...any) *Error {
	return &Error{Code: CodeValidation, Kind: KindValidation, Subject: subject, Msg: fmt.Sprintf(format, a...)}
}

// Internalf builds a CodeInternal error (unexpected failure) under the generic
// KindInternal. subject as in Validationf.
func Internalf(subject, format string, a ...any) *Error {
	return &Error{Code: CodeInternal, Kind: KindInternal, Subject: subject, Msg: fmt.Sprintf(format, a...)}
}

// KindForCode returns the generic kind for an exit code — the fallback class
// for a failure that summarizes others (e.g. apply's worst-of report) and so
// has no single specific kind of its own.
func KindForCode(c Code) string {
	switch c {
	case CodeNotFound:
		return KindNotFound
	case CodeValidation:
		return KindValidation
	default:
		return KindInternal
	}
}

// ExitCode resolves any error to a process exit code. nil -> 0; a *furrow Error
// -> its Code; anything else -> CodeInternal (an unclassified failure is
// internal by definition).
func ExitCode(err error) int {
	if err == nil {
		return int(CodeOK)
	}
	var fe *Error
	if errors.As(err, &fe) {
		return int(fe.Code)
	}
	return int(CodeInternal)
}

// AsError returns the *Error in err's chain, or nil. Useful when the CLI needs
// the id / structured fields rather than just the exit code.
func AsError(err error) *Error {
	var fe *Error
	if errors.As(err, &fe) {
		return fe
	}
	return nil
}
