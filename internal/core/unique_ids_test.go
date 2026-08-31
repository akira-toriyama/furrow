package core

import (
	"errors"
	"strings"
	"testing"
)

// CheckUniqueIDs guards Save's shards-keyed-by-id invariant: a duplicate would
// collapse to one file and the sweep would delete the loser. The contract worth
// pinning is the refusal shape — validation (exit 2, do-not-retry), the subject
// naming a duplicated id, every duplicated id in the message, and the `furrow
// lint` pointer — plus the boundary that a unique board passes untouched.
func TestCheckUniqueIDs(t *testing.T) {
	mk := func(ids ...string) *Index {
		idx := &Index{}
		for _, id := range ids {
			idx.Tasks = append(idx.Tasks, Task{ID: id})
		}
		return idx
	}

	if err := CheckUniqueIDs(mk()); err != nil {
		t.Errorf("empty index: %v", err)
	}
	if err := CheckUniqueIDs(mk("t-a", "t-b", "t-c")); err != nil {
		t.Errorf("unique ids: %v", err)
	}

	err := CheckUniqueIDs(mk("t-b", "t-a", "t-b"))
	var fe *Error
	if !errors.As(err, &fe) || fe.Code != CodeValidation || fe.Kind != KindValidation {
		t.Fatalf("one duplicate: want validation error, got %v", err)
	}
	if fe.Subject != "t-b" {
		t.Errorf("subject = %q, want the duplicated id t-b", fe.Subject)
	}
	for _, want := range []string{"t-b", "furrow lint"} {
		if !strings.Contains(fe.Msg, want) {
			t.Errorf("message %q should mention %q", fe.Msg, want)
		}
	}

	err = CheckUniqueIDs(mk("t-z", "t-z", "t-a", "t-a", "t-m"))
	if !errors.As(err, &fe) {
		t.Fatalf("two duplicates: want *Error, got %v", err)
	}
	if fe.Subject != "t-a" {
		t.Errorf("subject = %q, want the smallest duplicated id (deterministic)", fe.Subject)
	}
	if !strings.Contains(fe.Msg, "t-a, t-z") {
		t.Errorf("message %q should name every duplicated id, sorted", fe.Msg)
	}
	if strings.Contains(fe.Msg, "t-m") {
		t.Errorf("message %q must not name the unique id t-m", fe.Msg)
	}
}
