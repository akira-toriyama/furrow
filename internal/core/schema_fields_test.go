package core

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

var updateFields = flag.Bool("update-fields", false, "rewrite testdata/shard-fields.golden")

// The struct fingerprint: every persisted type's json keys, in struct order, plus
// the layout version they belong to. Frozen in a golden file.
//
// Passthrough (passthrough.go) makes an old binary PRESERVE a field it does not
// know. It does not make it HONOUR one. An old furrow will faithfully carry a
// future `"blocked": true` through every write — and then still hand you that task
// in `furrow next`, and still let you close it. Preservation downgrades silent DATA
// LOSS to silent SEMANTIC MISBEHAVIOR. That is a real improvement (loss is
// unrecoverable; misbehavior is fixed by updating the binary) but it is NOT a
// licence to skip a version bump. Only SchemaVersion can say "refuse to operate".
//
// So the rule "bump SchemaVersion when you change the shard layout" needs teeth.
// Nobody can remember a rule that fails silently — and this one fails silently by
// construction: add a field, forget the bump, and every test on a fresh store
// still passes. This golden is the teeth. Change the shape of a shard and this
// test fails, naming the version you must bump.
//
// Worth knowing before you argue "but my field is purely additive": every field
// ever added to Task — value, effort, repos, reviewed, deps, refs, checklist,
// parent — is read by a query, a sort, or a lane decision. The "safe for an old
// binary to ignore" class has never once had a member. The default answer is BUMP.
func TestShardFieldsGolden(t *testing.T) {
	got := fingerprint()
	path := filepath.Join("testdata", "shard-fields.golden")

	if *updateFields {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v — run `go test ./internal/core -run TestShardFieldsGolden -update-fields`", err)
	}
	if got != string(want) {
		t.Errorf(`the on-disk shape of a shard changed.

--- want (%s)
%s
--- got
%s
A shard's layout is a CONTRACT with every other furrow that reads this board —
including the pinned release your CI runs. Unknown-key passthrough will make an
older binary PRESERVE your new field, but it will not make it UNDERSTAND it: it
will keep showing that task in `+"`furrow next`"+` and letting you close it, as if the
field were not there.

So, before you accept this diff:

  1. Bump core.SchemaVersion (task.go), and update docs/schema/ + the goldens in
     the same change. That is a FLAG DAY — every board goes read-only until
     `+"`furrow upgrade`"+` runs, and every pinned caller must be bumped FIRST. Plan it.
  2. Only skip the bump if NO query, sort, filter, or lane decision reads the new
     field — in which case passthrough alone carries it safely. Note that no field
     ever added to Task has met that bar.

Then accept the new shape:
  go test ./internal/core -run TestShardFieldsGolden -update-fields
`, path, want, got)
	}
}

// fingerprint renders each persisted type's json keys in struct order — the exact
// thing an older binary's decoder sees. Field ORDER matters (it is the on-disk key
// order), so this is a list, not a set.
func fingerprint() string {
	var b strings.Builder
	b.WriteString("# The on-disk shape of every furrow shard. See TestShardFieldsGolden.\n")
	b.WriteString("# Changing this file means changing what other furrows read.\n")
	b.WriteString("schema_version " + strconv.Itoa(SchemaVersion) + "\n")
	for _, ty := range []any{Task{}, Meta{}, RepoRecord{}, ChecklistItem{}} {
		rt := reflect.TypeOf(ty)
		b.WriteString("\n" + rt.Name() + "\n")
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue // unexported carriers (extras) are not on disk
			}
			line := "  " + name + " " + f.Type.String()
			if opts != "" {
				line += " (" + opts + ")"
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}
