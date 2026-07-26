// Package query parses furrow's `-q` typed-query DSL into a flat AST of AND-ed
// terms. It is PURE (stdlib only): it knows the GRAMMAR, not furrow's fields —
// binding a Term to a task / Index / Clock is internal/app's job (Compile). The
// language mirrors the GitHub Projects filter bar folded into one string:
//
//	whitespace between terms = AND
//	comma inside one value    = OR      (label:ui,dx)
//	a leading '-' on a term   = NOT     (-status:done)
//	has:FIELD / no:FIELD      = presence / emptiness
//	is:FLAG                   = a computed flag (actionable, open, …)
//	field:value               = a qualifier; value may be a comparison
//	                            (>=4), a range (2..4, *..3, 3..*), or a
//	                            comma-OR set; a bare word (no colon) is free text.
//
// Quotes ('…' or "…") protect whitespace, commas, colons, and the range
// separator inside a value, and each scalar REMEMBERS whether it was quoted
// (Value.Quoted): the app gives a quoted value on a text qualifier exact-match
// semantics (title:'Bug fix') where a bare one is a substring. The parser only
// records the fact — what it means is the binder's business.
//
// There is deliberately NO cross-field OR, no grouping/parentheses, and no
// in-query sort — GitHub's own ceiling, and the long tail is jq's job. So a v1
// query is a flat term list: a whitespace tokenizer plus a per-term classifier,
// not a recursive-descent grammar.
package query

import (
	"fmt"
	"strings"
)

// Kind classifies a term.
type Kind uint8

const (
	Qualifier Kind = iota // field:value — Field + Op + Values
	Presence              // has:FIELD / no:FIELD — Field, with Not carrying has-vs-no
	State                 // is:FLAG — Field is the flag name
	FreeText              // a bare word or "quoted phrase" — Text
)

// Op is the operator on a Qualifier's value.
type Op uint8

const (
	Eq      Op = iota // equality / OR-set membership (comma splits Values)
	Gt                // >  (Values[0])
	Ge                // >=
	Lt                // <
	Le                // <=
	Between           // A..B inclusive; Values = {lo, hi}; "*" = open end
)

// Value is one scalar in a qualifier's value position. Quoted records whether
// it was quoted in the source: on a text qualifier the app reads a quoted
// scalar as whole-field equality (title:'Bug fix') and a bare one as a
// substring; everywhere else quotes only protect separator characters and
// change nothing.
type Value struct {
	Text   string
	Quoted bool
}

// Term is one AND-ed clause of a query.
type Term struct {
	Kind   Kind
	Not    bool    // a leading '-' (for Presence, folds has/no: see parseTerm)
	Field  string  // qualifier / presence field, or is-flag name (lower-cased)
	Op     Op      // Qualifier only
	Values []Value // Qualifier: the OR-set, comparison scalar, or {lo,hi} range
	Text   string  // FreeText only (unquoted)
}

// Query is a flat list of terms, all AND-ed together.
type Query []Term

// ParseError is a positioned parse/type failure. The App maps it to an exit-2
// envelope (id "query-parse"); Field names the offending qualifier when known so
// a caller can attach did-you-mean candidates.
type ParseError struct {
	Msg   string
	Term  string // the raw term that failed ("" for a whole-string fault)
	Field string // the qualifier field, when the fault is field-specific
}

func (e *ParseError) Error() string {
	if e.Term != "" {
		return fmt.Sprintf("%s (in %q)", e.Msg, e.Term)
	}
	return e.Msg
}

// Parse lexes s and classifies each term. An empty/whitespace-only query is a
// valid Query with no terms (matches everything). It never panics on arbitrary
// input — the fuzz target pins that.
func Parse(s string) (Query, error) {
	raws, err := lex(s)
	if err != nil {
		return nil, err
	}
	q := make(Query, 0, len(raws))
	for _, raw := range raws {
		t, err := parseTerm(raw)
		if err != nil {
			return nil, err
		}
		q = append(q, t)
	}
	return q, nil
}

// lex splits s into raw term strings on unquoted whitespace. A single- or
// double-quoted run may contain spaces (and the other quote char); the quotes
// are kept in the raw term so parseTerm can tell a quoted value from a bare one.
// An unterminated quote is a parse error.
func lex(s string) ([]string, error) {
	var terms []string
	var b strings.Builder
	inTerm := false
	var quote rune // 0 = not in quotes, else the opening quote rune
	flush := func() {
		if inTerm {
			terms = append(terms, b.String())
			b.Reset()
			inTerm = false
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			b.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			inTerm = true
			quote = r
			b.WriteRune(r)
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			inTerm = true
			b.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, &ParseError{Msg: "unterminated quote", Term: s}
	}
	flush()
	return terms, nil
}

// parseTerm classifies one raw term.
func parseTerm(raw string) (Term, error) {
	not := false
	if strings.HasPrefix(raw, "-") && len(raw) > 1 {
		not = true
		raw = raw[1:]
	}

	field, rest, hasColon := splitQualifier(raw)
	if !hasColon {
		// Free text: a bare word or a quoted phrase. Unquote for matching —
		// quoted-ness carries no extra meaning here (a phrase is a substring
		// match either way; the quotes only admit whitespace).
		v, err := scalar(raw)
		if err != nil {
			return Term{}, err
		}
		if v.Text == "" {
			return Term{}, &ParseError{Msg: "empty term", Term: raw}
		}
		return Term{Kind: FreeText, Not: not, Text: v.Text}, nil
	}

	lf := strings.ToLower(field)
	switch lf {
	case "has", "no":
		// no:X == has:X negated; a leading '-' toggles it, so `-no:X` == has:X.
		if rest == "" {
			return Term{}, &ParseError{Msg: lf + ": needs a field name", Term: raw}
		}
		return Term{Kind: Presence, Not: not != (lf == "no"), Field: strings.ToLower(rest)}, nil
	case "is":
		if rest == "" {
			return Term{}, &ParseError{Msg: "is: needs a flag", Term: raw}
		}
		return Term{Kind: State, Not: not, Field: strings.ToLower(rest)}, nil
	default:
		return parseQualifier(lf, rest, not, raw)
	}
}

// splitQualifier splits a raw term on its FIRST top-level (unquoted) colon into
// field + rest. A colon inside quotes (title:'a:b') is not a separator. Returns
// hasColon=false for a bare word.
func splitQualifier(raw string) (field, rest string, hasColon bool) {
	var quote rune
	for i, r := range raw {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ':':
			return raw[:i], raw[i+1:], true
		}
	}
	return raw, "", false
}

// parseQualifier turns the value part of field:value into Op + Values.
func parseQualifier(field, rest string, not bool, raw string) (Term, error) {
	if rest == "" {
		return Term{}, &ParseError{Msg: "qualifier " + field + ": needs a value", Term: raw, Field: field}
	}
	t := Term{Kind: Qualifier, Not: not, Field: field, Op: Eq}

	// Comparison: a leading >, >=, <, <=.
	for _, c := range []struct {
		pre string
		op  Op
	}{{">=", Ge}, {"<=", Le}, {">", Gt}, {"<", Lt}} {
		if strings.HasPrefix(rest, c.pre) {
			v := strings.TrimPrefix(rest, c.pre)
			if v == "" {
				return Term{}, &ParseError{Msg: "comparison " + c.pre + " needs a value", Term: raw, Field: field}
			}
			uv, err := scalar(v)
			if err != nil {
				return Term{}, err
			}
			t.Op = c.op
			t.Values = []Value{uv}
			return t, nil
		}
	}

	// Range A..B (either end may be "*" for open). Only a top-level ".." is a
	// separator — one inside quotes (title:'a..b') is value content.
	if lo, hi, ok := cutRange(rest); ok {
		if lo == "" || hi == "" {
			return Term{}, &ParseError{Msg: "range needs both bounds (lo..hi; use * for an open end)", Term: raw, Field: field}
		}
		ulo, err := scalar(lo)
		if err != nil {
			return Term{}, err
		}
		uhi, err := scalar(hi)
		if err != nil {
			return Term{}, err
		}
		t.Op = Between
		t.Values = []Value{ulo, uhi}
		return t, nil
	}

	// Equality / OR-set: comma splits, quotes protect (a value may contain a comma).
	vals, err := splitOrList(rest)
	if err != nil {
		return Term{}, err
	}
	if len(vals) == 0 {
		return Term{}, &ParseError{Msg: "qualifier " + field + ": needs a value", Term: raw, Field: field}
	}
	t.Values = vals
	return t, nil
}

// cutRange splits s on its first top-level (unquoted) ".." into the two range
// bounds. A ".." inside quotes is content, not syntax, so a quoted value may
// legitimately contain one. The scan is byte-wise: every character that matters
// (quote, dot) is ASCII, so multi-byte runes can never false-match.
func cutRange(s string) (lo, hi string, ok bool) {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '.' && i+1 < len(s) && s[i+1] == '.':
			return s[:i], s[i+2:], true
		}
	}
	return s, "", false
}

// splitOrList splits a value on top-level (unquoted) commas, unquoting each part.
func splitOrList(s string) ([]Value, error) {
	var out []Value
	var b strings.Builder
	var quote rune
	push := func() error {
		v, err := scalar(b.String())
		if err != nil {
			return err
		}
		if v.Text != "" {
			out = append(out, v)
		}
		b.Reset()
		return nil
	}
	for _, r := range s {
		switch {
		case quote != 0:
			b.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
			b.WriteRune(r)
		case r == ',':
			if err := push(); err != nil {
				return nil, err
			}
		default:
			b.WriteRune(r)
		}
	}
	if err := push(); err != nil {
		return nil, err
	}
	return out, nil
}

// scalar strips a single wrapping pair of matching quotes, if present, leaving
// inner content verbatim and recording the quoting in Value.Quoted. A bare
// (unquoted) value is returned trimmed of spaces. A stray opening quote with no
// close is a parse error.
func scalar(s string) (Value, error) {
	if len(s) >= 2 {
		if (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
			return Value{Text: s[1 : len(s)-1], Quoted: true}, nil
		}
	}
	if strings.ContainsAny(s, "'\"") {
		// A quote char that is not a clean wrapping pair — reject rather than
		// silently mangle (e.g. `title:'unterminated`).
		return Value{}, &ParseError{Msg: "mismatched quotes", Term: s}
	}
	return Value{Text: strings.TrimSpace(s)}, nil
}
