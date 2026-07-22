package query

import (
	"reflect"
	"testing"
)

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
			Query{{Kind: Qualifier, Field: "status", Op: Eq, Values: []string{"ready"}}},
		},
		{
			"lane alias keeps field verbatim",
			"lane:done",
			Query{{Kind: Qualifier, Field: "lane", Op: Eq, Values: []string{"done"}}},
		},
		{
			"comma OR set",
			"label:ui,dx",
			Query{{Kind: Qualifier, Field: "label", Op: Eq, Values: []string{"ui", "dx"}}},
		},
		{
			"two terms AND",
			"status:ready label:cli",
			Query{
				{Kind: Qualifier, Field: "status", Op: Eq, Values: []string{"ready"}},
				{Kind: Qualifier, Field: "label", Op: Eq, Values: []string{"cli"}},
			},
		},
		{
			"negation",
			"-status:done,icebox",
			Query{{Kind: Qualifier, Not: true, Field: "status", Op: Eq, Values: []string{"done", "icebox"}}},
		},
		{
			"comparison ge",
			"value:>=4",
			Query{{Kind: Qualifier, Field: "value", Op: Ge, Values: []string{"4"}}},
		},
		{
			"comparison lt",
			"effort:<2",
			Query{{Kind: Qualifier, Field: "effort", Op: Lt, Values: []string{"2"}}},
		},
		{
			"closed range",
			"value:2..4",
			Query{{Kind: Qualifier, Field: "value", Op: Between, Values: []string{"2", "4"}}},
		},
		{
			"open range low",
			"effort:*..3",
			Query{{Kind: Qualifier, Field: "effort", Op: Between, Values: []string{"*", "3"}}},
		},
		{
			"open range high",
			"value:3..*",
			Query{{Kind: Qualifier, Field: "value", Op: Between, Values: []string{"3", "*"}}},
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
			"quoted value keeps spaces",
			`title:'Bug fix'`,
			Query{{Kind: Qualifier, Field: "title", Op: Eq, Values: []string{"Bug fix"}}},
		},
		{
			"colon inside quoted value is not a separator",
			`title:'a:b'`,
			Query{{Kind: Qualifier, Field: "title", Op: Eq, Values: []string{"a:b"}}},
		},
		{
			"id prefix",
			"id:t-k3",
			Query{{Kind: Qualifier, Field: "id", Op: Eq, Values: []string{"t-k3"}}},
		},
		{
			"case-insensitive field, verbatim value",
			"STATUS:Ready",
			Query{{Kind: Qualifier, Field: "status", Op: Eq, Values: []string{"Ready"}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Parse(%q)\n got  %#v\n want %#v", tc.in, got, tc.want)
			}
		})
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
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = Parse(s) // must not panic; error is fine
	})
}
