package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/app"
	"github.com/akira-toriyama/furrow/v9/internal/core"
)

func readBoardConfig(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(os.Getenv(app.EnvDir), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestConfigSetBoardKey covers t-13mm's board half: the resolved board's
// config.toml gains exactly the one edit, the loader honors it, and the
// envelope reports {file, key, before, after, changed}.
func TestConfigSetBoardKey(t *testing.T) {
	initStore(t)
	before := readBoardConfig(t)

	out, code := run(t, "--json", "config", "set", "lanes.default", "ready")
	if code != 0 {
		t.Fatalf("config set exit = %d:\n%s", code, out)
	}
	var env struct {
		File    string   `json:"file"`
		Key     string   `json:"key"`
		Before  any      `json:"before"`
		After   any      `json:"after"`
		Changed []string `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, out)
	}
	if env.Key != "lanes.default" || env.Before != "inbox" || env.After != "ready" || len(env.Changed) != 1 {
		t.Errorf("envelope = %+v, want inbox→ready changed", env)
	}

	after := readBoardConfig(t)
	if !strings.Contains(after, `default = "ready"`) {
		t.Errorf("config.toml must carry the new value:\n%s", after)
	}
	// Surgical: the template's comment mass survives — the docs differ by the
	// one value only.
	if strings.Count(before, "#") != strings.Count(after, "#") {
		t.Error("comments must survive a set")
	}

	id := addTask(t, "post-set task")
	o, _ := run(t, "--json", "show", id, "--no-body")
	if !strings.Contains(o, `"status": "ready"`) {
		t.Errorf("the set default lane must take effect:\n%s", o)
	}

	out, code = run(t, "--json", "config", "set", "lanes.default", "ready")
	if code != 0 {
		t.Fatalf("no-op set exit = %d:\n%s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Changed) != 0 {
		t.Errorf("a no-op set must report changed [], got %+v", env.Changed)
	}
}

// TestConfigSetRefusals: the writer is strict — unknown key exits 2 with the
// vocabulary, a value the reader would clamp exits 2 unwritten, a bad type
// exits 2.
func TestConfigSetRefusals(t *testing.T) {
	initStore(t)
	before := readBoardConfig(t)

	fe, _ := runErr(t, "config", "set", "lanes.defalt", "ready")
	if fe == nil || fe.Code != core.CodeValidation || len(fe.Candidates) == 0 {
		t.Errorf("unknown key must be exit 2 with candidates, got %+v", fe)
	}
	found := false
	for _, c := range fe.Candidates {
		if c == "lanes.default" {
			found = true
		}
	}
	if !found {
		t.Errorf("candidates must carry the vocabulary, got %v", fe.Candidates)
	}

	// priority.step 0 is out of range — the reader would clamp it with a
	// warning, so the writer refuses it outright.
	if fe, _ := runErr(t, "config", "set", "priority.step", "0"); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("a clamp-bound value must be refused, got %+v", fe)
	}
	if fe, _ := runErr(t, "config", "set", "labels.required", "yes"); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("a non-bool for a bool key must be exit 2, got %+v", fe)
	}
	if readBoardConfig(t) != before {
		t.Error("a refused set must write nothing")
	}
}

// TestConfigSetAliasAndList: a dynamic alias.<name> key and a comma-split list.
func TestConfigSetAliasAndList(t *testing.T) {
	initStore(t)
	if out, code := run(t, "config", "set", "alias.triage", "ls -s inbox"); code != 0 {
		t.Fatalf("alias set exit = %d:\n%s", code, out)
	}
	if !strings.Contains(readBoardConfig(t), "triage = \"ls -s inbox\"") {
		t.Errorf("alias must land in [alias]:\n%s", readBoardConfig(t))
	}
	if out, code := run(t, "config", "set", "next.lanes", "backlog,ready"); code != 0 {
		t.Fatalf("list set exit = %d:\n%s", code, out)
	}
	if !strings.Contains(readBoardConfig(t), `lanes = ["backlog", "ready"]`) {
		t.Errorf("list value must render as a TOML array:\n%s", readBoardConfig(t))
	}
}

// TestConfigSetUserEntry: --user edits the picked [[board]] entry; --board
// resolves by substring with candidates on ambiguity; --board without --user
// is exit 2.
func TestConfigSetUserEntry(t *testing.T) {
	initStore(t)
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	cfgDir := filepath.Join(home, "furrow")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userCfg := filepath.Join(cfgDir, "config.toml")
	doc := "# my boards\n\n[[board]]\npath = \"~/proj/.furrow\"\nscopes = [\"~/proj\"]\n\n[[board]]\npath = \"~/work/.furrow\"\nscopes = [\"~/work\"]\n"
	if err := os.WriteFile(userCfg, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := run(t, "--json", "config", "set", "--user", "--board", "work", "autocommit", "true")
	if code != 0 {
		t.Fatalf("user set exit = %d:\n%s", code, out)
	}
	got, _ := os.ReadFile(userCfg)
	if !strings.Contains(string(got), "scopes = [\"~/work\"]\nautocommit = true\n") {
		t.Errorf("the work entry must gain the key:\n%s", got)
	}
	if strings.Contains(strings.SplitN(string(got), "[[board]]", 3)[1], "autocommit") {
		t.Errorf("the proj entry must be untouched:\n%s", got)
	}

	// Ambiguity: both paths contain ".furrow".
	fe, _ := runErr(t, "config", "set", "--user", "--board", ".furrow", "autocommit", "true")
	if fe == nil || fe.Code != core.CodeValidation || len(fe.Candidates) != 2 {
		t.Errorf("an ambiguous --board must be exit 2 with both candidates, got %+v", fe)
	}
	if fe, _ := runErr(t, "config", "set", "--user", "autocommit", "true"); fe == nil || len(fe.Candidates) != 2 {
		t.Errorf("a multi-entry file needs --board, got %+v", fe)
	}
	if fe, _ := runErr(t, "config", "set", "--board", "work", "autocommit", "true"); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("--board without --user must be exit 2, got %+v", fe)
	}
	// User-entry vocabulary, not the board's: lanes.default is not an entry key.
	if fe, _ := runErr(t, "config", "set", "--user", "--board", "work", "lanes.default", "x"); fe == nil || len(fe.Candidates) == 0 {
		t.Errorf("an unknown user-entry key must carry ITS vocabulary, got %+v", fe)
	}
}

// An alias named after a builtin never runs (the builtin wins) and lint warns
// alias-shadow about it; the strict writer refuses it instead of writing a
// dead entry.
func TestConfigSetRefusesAnAliasThatShadowsABuiltin(t *testing.T) {
	initStore(t)
	before := readBoardConfig(t)
	for _, name := range []string{"ls", "list", "help"} {
		fe, _ := runErr(t, "config", "set", "alias."+name, "ls -s inbox")
		if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, name) {
			t.Errorf("alias.%s must be refused as a shadow, got %+v", name, fe)
		}
	}
	if readBoardConfig(t) != before {
		t.Error("a refused alias wrote the file")
	}
	if out, code := run(t, "config", "set", "alias.triage", "ls -s inbox"); code != 0 {
		t.Fatalf("a non-shadowing alias still sets: %d\n%s", code, out)
	}
}

// An empty list member is refused: "ready,,in-progress" used to drop the
// empty and report "unchanged" when the rest already matched.
func TestConfigSetRefusesAnEmptyListMember(t *testing.T) {
	initStore(t)
	if fe, _ := runErr(t, "config", "set", "next.lanes", "ready,,in-progress"); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("an empty list member must be exit 2, got %+v", fe)
	}
}

// A key TOML cannot spell bare, a padded header and a control character in a
// value are all written as TOML the reader parses — exit 0, never "report
// this".
func TestConfigSetWritesNonBareKeysPaddedHeadersAndControlCharacters(t *testing.T) {
	initStore(t)
	for _, name := range []string{"日本", "has space"} {
		if out, code := run(t, "config", "set", "alias."+name, "ls"); code != 0 {
			t.Fatalf("alias.%s exit = %d:\n%s", name, code, out)
		}
		if out, code := run(t, "config", "set", "alias."+name, "next"); code != 0 || !strings.Contains(out, "set") {
			t.Fatalf("re-set of alias.%s exit = %d:\n%s", name, code, out)
		}
	}
	cfg := readBoardConfig(t)
	if strings.Count(cfg, "日本") != 1 || !strings.Contains(cfg, `"日本" = "next"`) || !strings.Contains(cfg, `"has space" = "next"`) {
		t.Errorf("non-bare aliases must be quoted once each:\n%s", cfg)
	}
	padded := strings.Replace(cfg, "[lanes]", "[ lanes ]", 1)
	if err := os.WriteFile(filepath.Join(os.Getenv(app.EnvDir), "config.toml"), []byte(padded), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := run(t, "config", "set", "lanes.default", "ready"); code != 0 {
		t.Fatalf("set under a padded header exit = %d:\n%s", code, out)
	}
	if cfg := readBoardConfig(t); strings.Count(cfg, "lanes ]") != 1 || strings.Contains(cfg, "\n[lanes]") {
		t.Errorf("the padded header must be edited in place, not duplicated:\n%s", cfg)
	}
	if out, code := run(t, "config", "set", "alias.ctl", "ls\x01"); code != 0 {
		t.Fatalf("a control character in a value exit = %d:\n%s", code, out)
	}
	if o, code := run(t, "--json", "ls"); code != 0 {
		t.Errorf("the board must still read after those writes: %d\n%s", code, o)
	}
}
