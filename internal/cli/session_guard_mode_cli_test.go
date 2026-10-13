package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/core"
	"github.com/akira-toriyama/furrow/v9/internal/testutil"
)

// t-hyr0, whole stack: the user config's [session].guard decides what a
// working occupant costs a later session — refuse (the default), a warning
// that says working (`warn`), or nothing at all (`off`).
func TestCLISessionGuardModeFromUserConfig(t *testing.T) {
	initStore(t)
	armGuard(t, 10*time.Second) // the occupant is mid-turn: busy under the default window

	t.Run("refuse is the default", func(t *testing.T) {
		testutil.UserConfig(t, "")
		fe, _ := runErr(t, "add", "into glyph", "-r", "o/glyph")
		if fe == nil || fe.Kind != core.KindSessionBusy {
			t.Fatalf("want session-busy, got %+v", fe)
		}
	})
	t.Run("warn never refuses and says working", func(t *testing.T) {
		testutil.UserConfig(t, "[session]\nguard = \"warn\"\n")
		stdout, stderr, code := runSplit(t, "--json", "add", "into glyph", "-r", "o/glyph")
		if code != 0 {
			t.Fatalf("exit %d:\n%s%s", code, stdout, stderr)
		}
		// `add --json` keeps stdout the created task; the warning is the one
		// session_warn JSON line on stderr (the error envelope's reader
		// parses it the same way).
		var line struct {
			Warn struct {
				Message string              `json:"message"`
				Clashes []core.SessionClash `json:"clashes"`
			} `json:"session_warn"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(stderr)), &line); err != nil {
			t.Fatalf("stderr under --json is one session_warn line: %v\n%q", err, stderr)
		}
		if len(line.Warn.Clashes) != 1 || !line.Warn.Clashes[0].Busy {
			t.Errorf("session_warn must carry the WORKING occupant: %+v", line.Warn)
		}
		if !strings.Contains(line.Warn.Message, "is being worked on") || !strings.Contains(line.Warn.Message, "session guard = warn") {
			t.Errorf("the line must say the occupant is working under warn mode: %q", line.Warn.Message)
		}
	})
	t.Run("off reads no registry", func(t *testing.T) {
		testutil.UserConfig(t, "[session]\nguard = \"off\"\n")
		stdout, stderr, code := runSplit(t, "--json", "add", "into glyph", "-r", "o/glyph")
		if code != 0 {
			t.Fatalf("exit %d:\n%s%s", code, stdout, stderr)
		}
		if strings.Contains(stdout, "session_warn") || strings.Contains(stderr, "session") {
			t.Errorf("off is silent — no envelope key, no stderr line, no note:\n%s%s", stdout, stderr)
		}
	})
	t.Run("the busy window is the user config's", func(t *testing.T) {
		testutil.UserConfig(t, "[session]\nbusy_seconds = 5\n")
		fe, _ := runErr(t, "add", "into glyph", "-r", "o/glyph")
		if fe != nil {
			t.Fatalf("an occupant silent past a 5s window is idle, got %+v", fe)
		}
	})
}

// `config set --user` writes the [session] keys (appending the section when
// the file has none), refuses them beside --board, refuses a value the reader
// would clamp, and no longer knows them on the board side.
func TestCLIConfigSetUserSessionKeys(t *testing.T) {
	initStore(t)
	userCfg := testutil.UserConfig(t, "# mine\n\n[[board]]\npath = \"~/proj/.furrow\"\nscopes = [\"~/proj\"]\n")

	out, code := run(t, "--json", "config", "set", "--user", "session.guard", "warn")
	if code != 0 {
		t.Fatalf("set exit = %d:\n%s", code, out)
	}
	got, _ := os.ReadFile(userCfg)
	if !strings.HasSuffix(string(got), "scopes = [\"~/proj\"]\n\n[session]\nguard = \"warn\"\n") {
		t.Errorf("a missing [session] is appended after the entries:\n%s", got)
	}
	if out, code := run(t, "config", "set", "--user", "session.busy_seconds", "45"); code != 0 {
		t.Fatalf("second key exit = %d:\n%s", code, out)
	}
	got, _ = os.ReadFile(userCfg)
	if !strings.Contains(string(got), "[session]\nguard = \"warn\"\nbusy_seconds = 45\n") {
		t.Errorf("the second key joins the section:\n%s", got)
	}
	if !strings.HasPrefix(string(got), "# mine\n\n[[board]]\npath = \"~/proj/.furrow\"\n") {
		t.Errorf("every other byte survives:\n%s", got)
	}

	fe, _ := runErr(t, "config", "set", "--user", "--board", "proj", "session.guard", "off")
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "--board") {
		t.Errorf("a [session] key beside --board is exit 2 naming the flag, got %+v", fe)
	}
	fe, _ = runErr(t, "config", "set", "--user", "session.guard", "maybe")
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "refuse|warn|off") {
		t.Errorf("a guard outside the vocabulary is refused before the write, got %+v", fe)
	}
	fe, _ = runErr(t, "config", "set", "--user", "session.busy_seconds", "0")
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "guard") {
		t.Errorf("a zero window is refused and points at guard = warn, got %+v", fe)
	}
	got, _ = os.ReadFile(userCfg)
	if !strings.Contains(string(got), "guard = \"warn\"\nbusy_seconds = 45\n") {
		t.Errorf("the refusals wrote nothing:\n%s", got)
	}

	// An unknown --user key lists BOTH vocabularies.
	fe, _ = runErr(t, "config", "set", "--user", "nope", "1")
	if fe == nil || len(fe.Candidates) == 0 {
		t.Fatalf("unknown user key must carry candidates, got %+v", fe)
	}
	joined := strings.Join(fe.Candidates, ",")
	if !strings.Contains(joined, "session.guard") || !strings.Contains(joined, "autocommit") {
		t.Errorf("candidates = %v, want the [session] keys and the entry keys", fe.Candidates)
	}

	// The board config has no [session] any more.
	fe, _ = runErr(t, "config", "set", "session.busy_seconds", "1")
	if fe == nil || fe.Code != core.CodeValidation || strings.Contains(strings.Join(fe.Candidates, ","), "session") {
		t.Errorf("session.* is not a board key, got %+v", fe)
	}
}
