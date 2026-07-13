package core

import (
	"strings"
	"testing"
)

// The READ gate refuses a board layout NEWER than the binary (a lenient
// unmarshal would silently drop fields the old binary doesn't know, then write
// the loss back). Current and older versions read fine.
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
	fe := AsError(err)
	if fe == nil || fe.ID != "schema-too-new" {
		t.Fatalf("error id should be \"schema-too-new\", got %+v", fe)
	}
	d, ok := fe.Details.(map[string]any)
	if !ok {
		t.Fatalf("details should be machine-actionable, got %#v", fe.Details)
	}
	if d["board_schema"] != SchemaVersion+1 || d["binary_schema"] != SchemaVersion {
		t.Errorf("details = %#v, want board_schema=%d binary_schema=%d", d, SchemaVersion+1, SchemaVersion)
	}
}

// The WRITE gate is stricter than the read gate, and that asymmetry is the fix
// for the 2026-07-13 outage: a binary writes ONLY a board that already declares
// its exact layout. An older board (or one with no meta at all) is readable but
// read-only until `furrow upgrade` deliberately raises it.
func TestCheckWritable(t *testing.T) {
	tests := []struct {
		name    string
		board   int
		wantID  string
		wantExt Code
	}{
		{"current board writes", SchemaVersion, "", CodeOK},
		{"older board is read-only", SchemaVersion - 1, "schema-upgrade-required", CodeValidation},
		{"unstamped board is read-only", 0, "schema-upgrade-required", CodeValidation},
		{"newer board refuses, and says so differently", SchemaVersion + 1, "schema-too-new", CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckWritable(tt.board)
			if tt.wantID == "" {
				if err != nil {
					t.Fatalf("CheckWritable(%d) = %v, want nil", tt.board, err)
				}
				return
			}
			fe := AsError(err)
			if fe == nil {
				t.Fatalf("CheckWritable(%d) = %v, want a furrow error", tt.board, err)
			}
			if fe.ID != tt.wantID {
				t.Errorf("id = %q, want %q", fe.ID, tt.wantID)
			}
			// The two refusals must be distinguishable by exit code alone: 2 = the
			// BOARD is stale (an explicit command fixes it); 3 = the BINARY is stale.
			if fe.Code != tt.wantExt {
				t.Errorf("exit code = %d, want %d", fe.Code, tt.wantExt)
			}
			d, ok := fe.Details.(map[string]any)
			if !ok || d["board_schema"] != tt.board || d["binary_schema"] != SchemaVersion {
				t.Errorf("details = %#v, want board_schema=%d binary_schema=%d", fe.Details, tt.board, SchemaVersion)
			}
		})
	}
}
