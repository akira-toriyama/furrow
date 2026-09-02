package cli

import "testing"

// `list` is the word a reader types first; both listing commands accept it as
// a cobra alias of `ls` with byte-identical output. Pinned here so dropping
// either alias is a test failure, not a help round-trip rediscovered in use.
func TestCLIListAliasesLs(t *testing.T) {
	initStore(t)
	addTask(t, "a task", "-r", "o/r")
	if _, code := run(t, "epic", "add", "a box", "-r", "o/r"); code != 0 {
		t.Fatal("epic add failed")
	}

	for _, tc := range [][2][]string{
		{{"ls", "--json"}, {"list", "--json"}},
		{{"epic", "ls", "--json"}, {"epic", "list", "--json"}},
	} {
		canonical, code := run(t, tc[0]...)
		if code != 0 {
			t.Fatalf("%v exit = %d:\n%s", tc[0], code, canonical)
		}
		aliased, code := run(t, tc[1]...)
		if code != 0 {
			t.Fatalf("%v exit = %d:\n%s", tc[1], code, aliased)
		}
		if aliased != canonical {
			t.Errorf("%v output differs from %v:\n%s\nvs\n%s", tc[1], tc[0], aliased, canonical)
		}
	}
}
