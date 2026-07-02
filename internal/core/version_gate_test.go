package core

import (
	"strings"
	"testing"
)

// The version gate refuses a board layout NEWER than the binary (a lenient
// unmarshal would silently drop fields the old binary doesn't know, then write
// the loss back). Current and older versions pass.
func TestCheckSchemaVersion(t *testing.T) {
	for _, v := range []int{0, 1, SchemaVersion} {
		if err := CheckSchemaVersion(v); err != nil {
			t.Errorf("CheckSchemaVersion(%d) = %v, want nil", v, err)
		}
	}

	err := CheckSchemaVersion(SchemaVersion + 1)
	if err == nil {
		t.Fatalf("CheckSchemaVersion(%d) = nil, want error", SchemaVersion+1)
	}
	if got := ExitCode(err); got != int(CodeInternal) {
		t.Errorf("exit code = %d, want %d (internal)", got, CodeInternal)
	}
	if !strings.Contains(err.Error(), "update the binary") {
		t.Errorf("message should tell the agent the fix (update the binary): %q", err.Error())
	}
	if fe := AsError(err); fe == nil || fe.ID != "meta" {
		t.Errorf("error id should be \"meta\", got %+v", fe)
	}
}
