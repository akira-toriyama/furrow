package query

import (
	"reflect"
	"strings"
	"testing"
)

// vals builds a bare (unquoted) Value list — the common case in the table.
func vals(texts ...string) []Value {
	out := make([]Value, len(texts))
	for i, s := range texts {
		out[i] = Value{Text: s}
	}
	return out
}

func TestParseTable(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Query
	}{
		{"empty", "", Query{}},
		{"whitespace only", "   \t ", Query{}},
		{
			"simple qualifier",
			"status:ready",
			Query{{Kind: Qualifier, Field: "status", Op: Eq, Values: vals("ready")}},
		},
		{
			"lane alias keeps field verbatim",
			"lane:done",
			Query{{Kind: Qualifier, Field: "lane", Op: Eq, Values: vals("done")}},
		},
		{
			"comma OR set",
			"label:ui,dx",
			Query{{Kind: Qualifier, Field: "label", Op: Eq, Values: vals("ui", "dx")}},
		},
		{
			"two terms AND",
			"status:ready label:cli",
			Query{
				{Kind: Qualifier, Field: "status", Op: Eq, Values: vals("ready")},
				{Kind: Qualifier, Field: "label", Op: Eq, Values: vals("cli")},
			},
		},
		{
			"negation",
			"-status:done,icebox",
			Query{{Kind: Qualifier, Not: true, Field: "status", Op: Eq, Values: vals("done", "icebox")}},
		},
		{
			"comparison ge",
			"value:>=4",
			Query{{Kind: Qualifier, Field: "value", Op: Ge, Values: vals("4")}},
		},
		{
			"comparison lt",
			"effort:<2",
			Query{{Kind: Qualifier, Field: "effort", Op: Lt, Values: vals("2")}},
		},
		{
			"closed range",
			"value:2..4",
			Query{{Kind: Qualifier, Field: "value", Op: Between, Values: vals("2", "4")}},
		},
		{
			"open range low",
			"effort:*..3",
			Query{{Kind: Qualifier, Field: "effort", Op: Between, Values: vals("*", "3")}},
		},
		{
			"open range high",
			"value:3..*",
			Query{{Kind: Qualifier, Field: "value", Op: Between, Values: vals("3", "*")}},
		},
		{
			"relative date comparison",
			"updated:>=-2w",
			Query{{Kind: Qualifier, Field: "updated", Op: Ge, Values: vals("-2w")}},
		},
		{
			"relative date range",
			"updated:-4w..-2w",
			Query{{Kind: Qualifier, Field: "updated", Op: Between, Values: vals("-4w", "-2w")}},
		},
		{
			"date range",
			"created:2026-07-01..2026-07-15",
			Query{{Kind: Qualifier, Field: "created", Op: Between, Values: vals("2026-07-01", "2026-07-15")}},
		},
		{
			"RFC3339 value keeps its colons",
			"created:>=2026-07-01T09:00:00Z",
			Query{{Kind: Qualifier, Field: "created", Op: Ge, Values: vals("2026-07-01T09:00:00Z")}},
		},
		{
			"presence has",
			"has:parent",
			Query{{Kind: Presence, Field: "parent"}},
		},
		{
			"presence no = has negated",
			"no:repo",
			Query{{Kind: Presence, Not: true, Field: "repo"}},
		},
		{
			"double negation -no = has",
			"-no:label",
			Query{{Kind: Presence, Not: false, Field: "label"}},
		},
		{
			"state flag",
			"is:actionable",
			Query{{Kind: State, Field: "actionable"}},
		},
		{
			"negated state",
			"-is:blocked",
			Query{{Kind: State, Not: true, Field: "blocked"}},
		},
		{
			"bare free word",
			"typed",
			Query{{Kind: FreeText, Text: "typed"}},
		},
		{
			"quoted phrase free text",
			`"typed query"`,
			Query{{Kind: FreeText, Text: "typed query"}},
		},
		{
			"quoted value keeps spaces and is marked quoted",
			`title:'Bug fix'`,
			Query{{Kind: Qualifier, Field: "title", Op: Eq, Values: []Value{{Text: "Bug fix", Quoted: true}}}},
		},
		{
			"colon inside quoted value is not a separator",
			`title:'a:b'`,
			Query{{Kind: Qualifier, Field: "title", Op: Eq, Values: []Value{{Text: "a:b", Quoted: true}}}},
		},
		{
			"range separator inside quotes is content",
			`title:'a..b'`,
			Query{{Kind: Qualifier, Field: "title", Op: Eq, Values: []Value{{Text: "a..b", Quoted: true}}}},
		},
		{
			"comma inside quotes is content",
			`title:'a,b'`,
			Query{{Kind: Qualifier, Field: "title", Op: Eq, Values: []Value{{Text: "a,b", Quoted: true}}}},
		},
		{
			"graph qualifier with hyphenated field",
			"depends-on:t-k3m9p",
			Query{{Kind: Qualifier, Field: "depends-on", Op: Eq, Values: vals("t-k3m9p")}},
		},
		{
			"id prefix",
			"id:t-k3",
			Query{{Kind: Qualifier, Field: "id", Op: Eq, Values: vals("t-k3")}},
		},
		{
			"case-insensitive field, verbatim value",
			"STATUS:Ready",
			Query{{Kind: Qualifier, Field: "status", Op: Eq, Values: vals("Ready")}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.in, err)
			}
			// Raw/Offset are positional bookkeeping asserted by
			// TestParseRecordsPositions; blank them so this table stays about
			// CLASSIFICATION.
			for i := range got {
				got[i].Raw, got[i].Offset = "", 0
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Parse(%q)\n got  %#v\n want %#v", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseRecordsPositions pins the position contract leg 1 of t-7th1 adds:
// every term carries its source text and byte offset, and a parse fault names
// the offending term with its offset — what a front-end needs to underline the
// token without re-lexing the query.
func TestParseRecordsPositions(t *testing.T) {
	q, err := Parse("status:ready  -label:ui 'quoted phrase'")
	if err != nil {
		t.Fatal(err)
	}
	wants := []struct {
		raw string
		off int
	}{{"status:ready", 0}, {"-label:ui", 14}, {"'quoted phrase'", 24}}
	if len(q) != len(wants) {
		t.Fatalf("terms = %d, want %d", len(q), len(wants))
	}
	for i, w := range wants {
		if q[i].Raw != w.raw || q[i].Offset != w.off {
			t.Errorf("term %d = %q@%d, want %q@%d", i, q[i].Raw, q[i].Offset, w.raw, w.off)
		}
	}

	for _, tc := range []struct {
		in   string
		term string
		off  int
	}{
		{"status:ready value:..", "value:..", 13},
		{"a :foo", ":foo", 2},
		{"x 'unterminated", "'unterminated", 2},
	} {
		_, err := Parse(tc.in)
		pe, ok := err.(*ParseError)
		if !ok {
			t.Fatalf("Parse(%q) err = %v, want *ParseError", tc.in, err)
		}
		if pe.Term != tc.term || pe.Offset != tc.off {
			t.Errorf("Parse(%q) fault at %q@%d, want %q@%d", tc.in, pe.Term, pe.Offset, tc.term, tc.off)
		}
	}
}

// TestParseEmptyFieldIsParseFault pins leg 3: `:foo` is a SHAPE fault (the
// colon promises a qualifier but names no field), classified with its parse
// siblings — not an unknown-field error offering candidates for "".
func TestParseEmptyFieldIsParseFault(t *testing.T) {
	_, err := Parse(":foo")
	pe, ok := err.(*ParseError)
	if !ok {
		t.Fatalf("err = %v, want *ParseError", err)
	}
	if !strings.Contains(pe.Msg, "field name") {
		t.Errorf("msg = %q, want it to explain the missing field name", pe.Msg)
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		`title:'unterminated`, // stray quote
		`"open phrase`,        // unterminated quote (whole string)
		`value:>`,             // comparison without a value
		`value:..4`,           // range missing lo
		`value:4..`,           // range missing hi
		`label:`,              // qualifier without a value
		`is:`,                 // is: without a flag
		`has:`,                // has: without a field
	}
	for _, in := range bad {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) should have errored", in)
		}
	}
}

// FuzzParse pins that Parse never panics on arbitrary input (it either returns a
// Query or a ParseError). The parser is the only place untrusted query text
// meets furrow, so panic-freedom is a hard requirement.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"", "status:ready", "-label:a,b", "value:>=4", "x:1..2", `title:'a b'`,
		"is:actionable has:parent -no:repo", `"phrase"`, "::::", "a:b:c", "--", "-",
		",", "value:*..*", `'`, `"`, "\t\n", "label:,,,",
		"updated:>=-2w", "created:2026-07-01..2026-07-15", "closed:<-30d",
		"created:>=2026-07-01T09:00:00Z", "reviewed:*..-1d", "is:stale",
		"depends-on:t-1,t-2", "blocks:t-x", "child-of:t-y", `title:'a..b'`,
		"updated:..", "updated:-", "updated:-2x", "created:'..'",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = Parse(s) // must not panic; error is fine
	})
}
