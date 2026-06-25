package core

import (
	"errors"
	"fmt"
)

// Code is furrow's process exit-code contract (MEMO §4 / ROADMAP §8). The CLI
// maps a returned error to one of these on the way out. Keep the meanings
// stable — scripts and Claude Code branch on them.
type Code int

const (
	CodeOK         Code = 0 // success
	CodeNotFound   Code = 1 // a requested id / result set was empty
	CodeValidation Code = 2 // bad usage or invalid input — fix the args, do not retry
	CodeInternal   Code = 3 // internal / IO failure
)

// Error is furrow's structured error. On a non-zero exit the CLI prints it to
// stderr as {"error":{"code","id","message"}} so callers get a machine-readable
// failure (MEMO §4). Plain (non-*Error) errors are treated as CodeInternal.
type Error struct {
	Code Code
	ID   string // the offending task id, "index", or "" when not applicable
	Msg  string
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
