package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// App.Loc had no production assignment at all: it was declared as "the
// OPERATOR's zone" and only ever set by tests, so every real invocation read
// dates in the zone of whatever process ran the command. On a shared board that
// CI also writes to, the same `--due 2026-08-04` therefore bound 23:59:59+09:00
// from the operator's machine and 23:59:59Z from the runner.
func TestBoardTimezoneBindsDueOnDisk(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	// Declare the zone the way an operator would: by uncommenting the line
	// `furrow init` already wrote. Asserting the substitution landed pins that
	// the shipped template still carries the key.
	cfgPath := filepath.Join(dir, ".furrow", "config.toml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	declared := strings.Replace(string(data), `# timezone = "Asia/Tokyo"`, `timezone = "Asia/Tokyo"`, 1)
	if declared == string(data) {
		t.Fatal("the init template no longer carries a commented [due].timezone line")
	}
	if err := os.WriteFile(cfgPath, []byte(declared), 0o600); err != nil {
		t.Fatal(err)
	}

	a := openBoard(t, dir)
	if a.Loc == nil {
		t.Fatal("Open left Loc nil — the board's declared zone never reached the App")
	}
	if a.Cfg.DueTimezoneName != "Asia/Tokyo" {
		t.Fatalf("config zone name = %q, want Asia/Tokyo", a.Cfg.DueTimezoneName)
	}

	// A bare date binds the END of that day, and "that day" is the board's.
	got, err := a.parseDue("2026-08-04")
	if err != nil {
		t.Fatalf("parseDue: %v", err)
	}
	want := time.Date(2026, 8, 4, 14, 59, 59, 0, time.UTC) // 23:59:59 +09:00
	if !got.Equal(want) {
		t.Errorf("parseDue = %s, want %s — the process zone won over the board's",
			got.UTC().Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// An undeclared zone must keep the old behaviour exactly, so adding the key
// rewrites nothing on the boards that never set it.
func TestUndeclaredTimezoneKeepsTheProcessZone(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	a := openBoard(t, dir)
	if a.Loc != nil {
		t.Errorf("Loc = %v, want nil so loc() falls through to time.Local", a.Loc)
	}
	if a.loc() != time.Local {
		t.Errorf("loc() = %v, want time.Local", a.loc())
	}
}

// NewWithStore takes a Config too, so a dry-run or an in-memory App must read
// the same calendar as the on-disk one it stands in for.
func TestNewWithStoreHonorsTheConfiguredZone(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC))
	if a.loc() != jst {
		t.Errorf("loc() = %v, want the configured zone %v", a.loc(), jst)
	}
}

// `"Local"` is the one value that LOADS and declares nothing: Go resolves it to
// the running machine's zone. It therefore set DueTimezone non-nil — the exact
// test lint reads to decide whether a shared board declared a calendar — so the
// key silenced repeat-no-timezone while leaving every date bound to whichever
// machine ran the command, which is the failure the key exists to close.
func TestALocalTimezoneDeclaresNoCalendar(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, ".furrow", "config.toml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	declared := strings.Replace(string(data), `# timezone = "Asia/Tokyo"`, `timezone = "Local"`, 1)
	if declared == string(data) {
		t.Fatal("the init template no longer carries a commented [due].timezone line")
	}
	if err := os.WriteFile(cfgPath, []byte(declared), 0o600); err != nil {
		t.Fatal(err)
	}

	a := openBoard(t, dir)
	if a.Loc != nil {
		t.Errorf("Loc = %v — a machine's own zone was accepted as the board's calendar", a.Loc)
	}
	if a.Cfg.DueTimezoneName != "" {
		t.Errorf("config zone name = %q, want empty: the board declared none", a.Cfg.DueTimezoneName)
	}

	// The clamp is loud, and the lint the key silenced is loud again.
	if _, err := a.Add("weekly chore", AddOpts{Due: "2027-05-05", Repeat: "weekly"}); err != nil {
		t.Fatal(err)
	}
	problems, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	var clamp, noTZ bool
	for _, p := range problems {
		switch p.Code {
		case "config-clamp":
			clamp = clamp || strings.Contains(p.Msg, "due.timezone")
		case "repeat-no-timezone":
			noTZ = true
		}
	}
	if !clamp {
		t.Errorf("no config-clamp problem named due.timezone: %+v", problems)
	}
	if !noTZ {
		t.Error("repeat-no-timezone stayed silent on a shared board whose only declaration was the machine's own zone")
	}
}

// A zone that IS a calendar still lands, including UTC — the declaration a CI
// board makes on purpose.
func TestARealZoneIsStillDeclarable(t *testing.T) {
	for _, name := range []string{"Asia/Tokyo", "UTC"} {
		dir := t.TempDir()
		if _, err := Init(dir); err != nil {
			t.Fatal(err)
		}
		cfgPath := filepath.Join(dir, ".furrow", "config.toml")
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		declared := strings.Replace(string(data), `# timezone = "Asia/Tokyo"`, `timezone = "`+name+`"`, 1)
		if err := os.WriteFile(cfgPath, []byte(declared), 0o600); err != nil {
			t.Fatal(err)
		}
		a := openBoard(t, dir)
		if a.Loc == nil || a.Cfg.DueTimezoneName != name {
			t.Errorf("%s: Loc=%v name=%q — a declarable calendar was clamped away", name, a.Loc, a.Cfg.DueTimezoneName)
		}
	}
}
