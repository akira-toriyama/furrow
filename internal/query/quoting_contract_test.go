package query

import "testing"

// The quoting rule the -q flag help and README state, pinned so the prose and
// the lexer cannot drift: either quote char wraps a value, the other may sit
// inside, there is no escape, and a value holding both is unrepresentable.
func TestQuotingRuleIsTheDocumentedOne(t *testing.T) {
	cases := []struct {
		in   string
		want string // the single value's text; "" = parse error expected
	}{
		{`title:"a, b: c .. d"`, "a, b: c .. d"},
		{`title:"it's"`, "it's"},
		{`title:'say "hi"'`, `say "hi"`},
		{`title:"back\slash stays"`, `back\slash stays`},
		{`title:"both ' and \" here"`, ""},
		{`title:'lone`, ""},
	}
	for _, c := range cases {
		q, err := Parse(c.in)
		if c.want == "" {
			if err == nil {
				t.Errorf("Parse(%s) = %+v, want a parse error", c.in, q)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%s): %v", c.in, err)
			continue
		}
		if len(q) != 1 || len(q[0].Values) != 1 || q[0].Values[0].Text != c.want || !q[0].Values[0].Quoted {
			t.Errorf("Parse(%s) = %+v, want one quoted value %q", c.in, q, c.want)
		}
	}
	if q, err := Parse(`title:"a b" is:open`); err != nil || len(q) != 2 {
		t.Errorf("a space outside quotes must start the next term: %+v, %v", q, err)
	}
}
