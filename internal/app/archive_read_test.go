package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

func TestArchivedReadGuardsNonFileBackedStore(t *testing.T) {
	a := newApp() // memstore: a.Dir == "", no archive on disk

	if _, _, err := a.GetBatchArchived([]string{"t-x"}, false); core.AsError(err) == nil || core.AsError(err).Code != core.CodeValidation {
		t.Fatalf("archived read on a non-file-backed store should be a validation error, got %v", err)
	}
	if _, err := a.List(QueryOpts{Archived: true}); core.AsError(err) == nil || core.AsError(err).Code != core.CodeValidation {
		t.Fatalf("ls --archived on a non-file-backed store should be a validation error, got %v", err)
	}
	// ArchivedContains is best-effort: never errors, just reports nothing archived.
	if got := a.ArchivedContains([]string{"t-x"}); got != nil {
		t.Errorf("ArchivedContains should be nil on a non-file-backed store, got %v", got)
	}
}
