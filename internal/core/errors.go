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
// stderr as {"error":{"code","id","message"[,"details"]}} so callers get a
// machine-readable failure. Plain (non-*Error) errors are treated as
// CodeInternal.
type Error struct {
	Code Code
	ID   string // the offending task id, "index", an error slug (e.g. "sync-conflict"), or ""
	Msg  string
	// Details is optional machine-actionable payload for errors where the
	// message alone isn't enough to act on (e.g. sync-conflict carries
	// {"paths": [...]}). Rendered as "details" in the envelope when non-nil;
	// existing consumers that only read code/id/message are unaffected.
	Details any
	// Candidates is the optional list of concrete alternatives when an input
	// almost resolved — an ambiguous repo short name, or the did-you-mean
	// guard's repo suggestion. Rendered as "candidates" in the envelope when
	// non-empty, so agents branch on the array and never regex the prose.
	Candidates []string
}

func (e *Error) Error() string { return e.Msg }

// NotFound builds a CodeNotFound error for a missing task id.
func NotFound(id string) *Error {
	return &Error{Code: CodeNotFound, ID: id, Msg: fmt.Sprintf("task not found: %s", id)}
}

// Validationf builds a CodeValidation error (bad input).
func Validationf(id, format string, a ...any) *Error {
	return &Error{Code: CodeValidation, ID: id, Msg: fmt.Sprintf(format, a...)}
}

// Internalf builds a CodeInternal error (unexpected failure).
func Internalf(id, format string, a ...any) *Error {
	return &Error{Code: CodeInternal, ID: id, Msg: fmt.Sprintf(format, a...)}
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
