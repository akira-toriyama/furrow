package config

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// This file is the WRITE side of the config contract: `furrow config set`, the
// one command allowed to touch a config.toml (the read side stays
// clamp-don't-reject; see Load). Two rules shape everything here:
//
//   - The edit is SURGICAL, git-config style. Go has no comment-preserving TOML
//     editor, and regenerating from the template would destroy the operator's
//     comments and ordering — the very things that make config.toml the
//     annotated canonical copy. So the document is edited line-wise: only the
//     target key's value span changes (or one line is inserted), every other
//     byte survives verbatim.
//   - The WRITER is strict where the READER is lenient. A read clamps an
//     unknown key with a warning because refusing would brick every command on
//     a half-written file; a WRITE of an unknown key would create it — and in
//     the passthrough world nothing ever deletes it again — so it is exit 2
//     with the key vocabulary in candidates instead.

// KeyKind is the CLI-value type of a config key — how `config set` parses the
// argument before rendering it as TOML.
type KeyKind int

const (
	KindString KeyKind = iota
	KindInt
	KindBool
	KindStringSlice
)

// Key is one settable config key: Section "" means a top-level key; Dynamic
// marks a section whose key names are operator-chosen ([alias]), where Name is
// the section itself.
type Key struct {
	Section string
	Name    string
	Kind    KeyKind
	Dynamic bool
}

// Dotted is the spelling `config set` takes: section.name, or the bare name
// for a top-level key. A dynamic section renders as section.<name>.
func (k Key) Dotted() string {
	if k.Section == "" {
		return k.Name
	}
	if k.Dynamic {
		return k.Section + ".<name>"
	}
	return k.Section + "." + k.Name
}

// BoardKeys is the settable-key vocabulary of a BOARD config.toml, derived by
// reflection from the same raw decode struct Load reads — the TopLevelKeys
// pattern: one registration, so the writer can never accept a key the reader
// ignores (or reject one it honors).
func BoardKeys() []Key { return structKeys(reflect.TypeOf(raw{})) }

// UserEntryKeys is the settable-key vocabulary of one [[board]] entry of the
// user-level config, derived from its raw decode struct the same way.
func UserEntryKeys() []Key { return structKeys(reflect.TypeOf(rawBoard{})) }

// UserKeys is the settable-key vocabulary of the user-level config's own
// sections ([session]) — everything in it that is not a [[board]] entry (an
// array of tables is addressed per entry, by UserEntryKeys).
func UserKeys() []Key { return structKeys(reflect.TypeOf(rawGlobal{})) }

func structKeys(t reflect.Type) []Key {
	var keys []Key
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("toml")
		if idx := strings.IndexByte(tag, ','); idx >= 0 {
			tag = tag[:idx]
		}
		if tag == "" || tag == "-" {
			continue
		}
		ft := f.Type
		if ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct {
			continue // an array of tables ([[board]]) is edited per entry
		}
		if ft.Kind() == reflect.Struct {
			for j := 0; j < ft.NumField(); j++ {
				sf := ft.Field(j)
				stag := sf.Tag.Get("toml")
				if idx := strings.IndexByte(stag, ','); idx >= 0 {
					stag = stag[:idx]
				}
				if stag == "" || stag == "-" {
					continue
				}
				// A map INSIDE a section is a dynamic SUB-table ([lint.severity]):
				// its Section is the dotted table header, so SetInSection edits the
				// literal `[section.subtable]` block and ResolveKey's prefix match
				// peels the operator-chosen key off the end.
				if sf.Type.Kind() == reflect.Map {
					keys = append(keys, Key{Section: tag + "." + stag, Name: tag + "." + stag, Kind: KindString, Dynamic: true})
					continue
				}
				keys = append(keys, Key{Section: tag, Name: stag, Kind: kindOf(sf.Type)})
			}
			continue
		}
		if ft.Kind() == reflect.Map {
			keys = append(keys, Key{Section: tag, Name: tag, Kind: KindString, Dynamic: true})
			continue
		}
		keys = append(keys, Key{Name: tag, Kind: kindOf(ft)})
	}
	return keys
}

func kindOf(t reflect.Type) KeyKind {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool:
		return KindBool
	case reflect.Int:
		return KindInt
	case reflect.Slice:
		return KindStringSlice
	default:
		return KindString
	}
}

// ResolveKey matches a dotted CLI key against a vocabulary: the exact dotted
// spelling, or a dynamic section's section.<anything> — where the section may
// itself be dotted ([lint.severity]), so a dynamic key is matched by PREFIX
// rather than by splitting the input at its first dot. A miss returns the full
// vocabulary for the caller's candidates array.
func ResolveKey(keys []Key, dotted string) (Key, string, bool) {
	for _, k := range keys {
		if k.Dynamic {
			if name, ok := strings.CutPrefix(dotted, k.Section+"."); ok && name != "" {
				return k, name, true
			}
			continue
		}
		if k.Dotted() == dotted {
			return k, k.Name, true
		}
	}
	return Key{}, "", false
}

// KeyVocabulary renders the dotted spellings for a candidates array.
func KeyVocabulary(keys []Key) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.Dotted())
	}
	return out
}

// ParseCLIValue turns the CLI argument into the key's typed value. A slice is
// comma-split (the -s/-l comma convention), items trimmed, empties dropped; a
// bool takes exactly true/false; an int must parse whole. The typed value is
// what the caller renders, reports in the envelope, and validates.
func ParseCLIValue(k Key, s string) (any, error) {
	switch k.Kind {
	case KindBool:
		switch s {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, fmt.Errorf("%q is not a bool (true|false)", s)
	case KindInt:
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", s)
		}
		return n, nil
	case KindStringSlice:
		// An empty member ("ready,,in-progress", a trailing comma) is refused,
		// not dropped: the strict writer must not silently write less than it
		// was given — the drop read as "unchanged" when the rest already matched.
		items := []string{}
		if strings.TrimSpace(s) == "" {
			return items, nil
		}
		for _, it := range strings.Split(s, ",") {
			it = strings.TrimSpace(it)
			if it == "" {
				return nil, fmt.Errorf("%q has an empty list member", s)
			}
			items = append(items, it)
		}
		return items, nil
	default:
		return s, nil
	}
}

// RenderTOMLValue renders a typed value as the TOML text the editor splices in.
func RenderTOMLValue(v any) string {
	switch x := v.(type) {
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case []string:
		parts := make([]string, 0, len(x))
		for _, s := range x {
			parts = append(parts, renderTOMLString(s))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return renderTOMLString(fmt.Sprint(v))
	}
}

// renderTOMLString emits a TOML basic string. Go's %q is ALMOST it, but Go
// spells exotic escapes as \xNN which TOML does not know — so escape the two
// structural characters by hand and refuse nothing (config values are lane
// names, repo slugs, command strings).
func renderTOMLString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			// A bare control character is not TOML; \uXXXX is, and reads back
			// as the same rune.
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// SetResult reports what the surgical edit did: the value text it replaced
// (Existed) or the fact it inserted, plus the whole edited document.
type SetResult struct {
	Doc       string // the edited document
	OldText   string // the previous value's TOML text, "" when !Existed
	Existed   bool   // the key was present before
	Unchanged bool   // the rendered value equals the old text — nothing to write
}

// SetInSection edits `key = value` inside [section] of doc (section "" = the
// top-level table), preserving every other byte: an existing key keeps its
// line, indentation, and trailing comment with only the value span replaced
// (a multi-line array collapses to the new one-line value); a missing key is
// inserted after the section's last non-blank line; a missing section is
// appended at EOF. Top-level inserts land before the first table header —
// TOML's own requirement.
func SetInSection(doc, section, key, rendered string) (SetResult, error) {
	lines := strings.Split(doc, "\n")
	start, end, found := sectionSpan(lines, section)
	if !found {
		if section == "" {
			return insertTopLevel(lines, key, rendered), nil
		}
		out := strings.TrimRight(doc, "\n")
		if out != "" {
			out += "\n\n"
		}
		out += "[" + section + "]\n" + renderKey(key) + " = " + rendered + "\n"
		return SetResult{Doc: out}, nil
	}
	if kl, ok := findKeyLine(lines, start, end, key); ok {
		return replaceValue(lines, kl, end, key, rendered)
	}
	// Insert after the section's last non-blank line (its header when empty),
	// so trailing blank lines keep separating it from the next section. start
	// is the header line, or -1 for the headerless top-level span — either way
	// the body begins at start+1 and an all-blank span inserts right after
	// start.
	at := start
	for i := start + 1; i < end; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			at = i
		}
	}
	lines = append(lines[:at+1], append([]string{renderKey(key) + " = " + rendered}, lines[at+1:]...)...)
	return SetResult{Doc: strings.Join(lines, "\n")}, nil
}

// UnsetInSection returns doc with `key`'s whole value span removed from
// [section] — the baseline the regression guard compares an edit against: the
// warnings a document carries WITH the key that the same document without it
// does not. A key that is not there leaves doc unchanged.
func UnsetInSection(doc, section, key string) string {
	lines := strings.Split(doc, "\n")
	start, end, found := sectionSpan(lines, section)
	if !found {
		return doc
	}
	return unsetKey(lines, start, end, key)
}

// UnsetInArrayTable is UnsetInSection for the index-th [[table]] block.
func UnsetInArrayTable(doc, table string, index int) func(key string) string {
	return func(key string) string {
		lines := strings.Split(doc, "\n")
		start, end, ok := arrayTableSpan(lines, table, index)
		if !ok {
			return doc
		}
		return unsetKey(lines, start, end, key)
	}
}

func unsetKey(lines []string, start, end int, key string) string {
	kl, ok := findKeyLine(lines, start, end, key)
	if !ok {
		return strings.Join(lines, "\n")
	}
	eq := strings.IndexByte(lines[kl], '=')
	_, _, lastLine, err := scanValue(lines, kl, eq+1, end)
	if err != nil {
		return strings.Join(lines, "\n")
	}
	out := append(append([]string{}, lines[:kl]...), lines[lastLine+1:]...)
	return strings.Join(out, "\n")
}

// SetInArrayTable edits `key = value` inside the index-th [[table]] block —
// the [[board]] entry case. The block must exist (creating entries is `config
// init`'s and the operator's job, not the writer's).
func SetInArrayTable(doc, table string, index int, key, rendered string) (SetResult, error) {
	lines := strings.Split(doc, "\n")
	start, end, ok := arrayTableSpan(lines, table, index)
	if !ok {
		return SetResult{}, fmt.Errorf("no [[%s]] entry #%d in the document", table, index+1)
	}
	if kl, ok := findKeyLine(lines, start, end, key); ok {
		return replaceValue(lines, kl, end, key, rendered)
	}
	at := start
	for i := start; i < end; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			at = i
		}
	}
	lines = append(lines[:at+1], append([]string{renderKey(key) + " = " + rendered}, lines[at+1:]...)...)
	return SetResult{Doc: strings.Join(lines, "\n")}, nil
}

// arrayTableSpan finds the index-th [[table]] block: [start(header), end).
func arrayTableSpan(lines []string, table string, index int) (int, int, bool) {
	n := -1
	start := -1
	for i, l := range lines {
		if name, isArray, ok := headerName(l); ok && isArray && name == table {
			n++
			if n == index {
				start = i
				break
			}
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	end := len(lines)
	for i := start + 1; i < end; i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "[") {
			end = i
			break
		}
	}
	return start, end, true
}

// headerName reads a table header line as TOML does: `[lanes]`, `[ lanes ]`
// and `["lanes"]` all name the table lanes, and `[[board]]` names an array
// table. ok is false for any other line. The editor used to match the literal
// `[section]` only, so a header the operator had padded (`[ lanes ]`) was
// never found: the key was appended under a SECOND `[lanes]` at EOF, which is
// a table defined twice, and the write failed as an internal error.
func headerName(line string) (name string, arrayTable bool, ok bool) {
	t := trimComment(strings.TrimSpace(line))
	if !strings.HasPrefix(t, "[") || !strings.HasSuffix(t, "]") {
		return "", false, false
	}
	inner := t[1 : len(t)-1]
	if strings.HasPrefix(inner, "[") && strings.HasSuffix(inner, "]") {
		arrayTable = true
		inner = inner[1 : len(inner)-1]
	}
	parts := strings.Split(inner, ".")
	for i, p := range parts {
		parts[i] = unquoteKey(strings.TrimSpace(p))
	}
	return strings.Join(parts, "."), arrayTable, true
}

// sectionSpan finds [section]'s body span [start(header), end) — end exclusive,
// stopping at the next table header. For the top-level table it is [0, first
// header), reported found only so callers can probe keys (start = -1 marks the
// headerless span).
func sectionSpan(lines []string, section string) (int, int, bool) {
	if section == "" {
		end := len(lines)
		for i, l := range lines {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "[") {
				end = i
				break
			}
		}
		return -1, end, true
	}
	start := -1
	for i, l := range lines {
		if name, isArray, ok := headerName(l); ok && !isArray && name == section {
			start = i
			break
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "[") {
			end = i
			break
		}
	}
	return start, end, true
}

// trimComment drops a trailing comment from an already-trimmed line — enough
// for header matching (a header line has no strings to confuse a '#' in).
func trimComment(t string) string {
	if idx := strings.IndexByte(t, '#'); idx >= 0 {
		return strings.TrimSpace(t[:idx])
	}
	return t
}

// findKeyLine scans span (start, end) for a `key =` line. A commented-out
// example (`# lanes = …`) deliberately does not match — the operator's
// documentation is not the operator's setting.
func findKeyLine(lines []string, start, end int, key string) (int, bool) {
	for i := start + 1; i < end; i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "#") || t == "" {
			continue
		}
		eq := strings.IndexByte(t, '=')
		if eq < 0 {
			continue
		}
		if unquoteKey(strings.TrimSpace(t[:eq])) == key {
			return i, true
		}
	}
	return 0, false
}

// bareKeyPattern is what TOML lets stand unquoted as a key.
var bareKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// renderKey spells a key for a line the editor writes: bare when TOML allows
// it, a basic string otherwise (an alias named 日本 or "has space" is a
// legitimate key — written bare it was not TOML at all, and the set failed
// as "report this").
func renderKey(key string) string {
	if bareKeyPattern.MatchString(key) {
		return key
	}
	return renderTOMLString(key)
}

// unquoteKey reads a key as written — bare, or a basic/literal string — back
// to its name, so findKeyLine matches the line renderKey wrote (and one the
// operator quoted by hand). Text that is not a quoted key comes back as is.
func unquoteKey(k string) string {
	if len(k) >= 2 && k[0] == '"' && k[len(k)-1] == '"' {
		if s, err := strconv.Unquote(k); err == nil {
			return s
		}
	}
	if len(k) >= 2 && k[0] == '\'' && k[len(k)-1] == '\'' {
		return k[1 : len(k)-1]
	}
	return k
}

// insertTopLevel places a top-level key before the first table header (TOML
// requires it), after any existing top-level content.
func insertTopLevel(lines []string, key, rendered string) SetResult {
	at := len(lines)
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "[") {
			at = i
			break
		}
	}
	// Land after the last non-blank line above the header, so the key joins the
	// top-level block instead of floating between blank lines.
	ins := 0
	for i := 0; i < at; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			ins = i + 1
		}
	}
	line := key + " = " + rendered
	lines = append(lines[:ins], append([]string{line}, lines[ins:]...)...)
	return SetResult{Doc: strings.Join(lines, "\n")}
}

// replaceValue splices rendered over the existing value span of the key at
// line kl, which may run over multiple lines for a bracketed array. The line's
// prefix (indentation, key, spacing around '=') and any trailing comment on
// the span's final line survive; a multi-line span collapses to one line.
func replaceValue(lines []string, kl, end int, key, rendered string) (SetResult, error) {
	line := lines[kl]
	eq := strings.IndexByte(line, '=')
	prefix := line[:eq+1]
	valText, comment, lastLine, err := scanValue(lines, kl, eq+1, end)
	if err != nil {
		return SetResult{}, err
	}
	old := strings.TrimSpace(valText)
	if old == rendered {
		return SetResult{Doc: strings.Join(lines, "\n"), OldText: old, Existed: true, Unchanged: true}, nil
	}
	// The whitespace around the value is the operator's too: the gap before an
	// inline comment (`width = 5 # t-k3m9p`) used to come back as two spaces,
	// one byte the help promised would survive. A span that collapses from
	// several lines keeps the first line's lead and the last line's gap.
	lead := valText[:len(valText)-len(strings.TrimLeft(valText, " \t"))]
	gap := valText[len(strings.TrimRight(valText, " \t")):]
	newLine := prefix + lead + rendered
	if comment != "" {
		if gap == "" {
			gap = " "
		}
		newLine += gap + comment
	}
	out := append(append([]string{}, lines[:kl]...), newLine)
	out = append(out, lines[lastLine+1:]...)
	return SetResult{Doc: strings.Join(out, "\n"), OldText: old, Existed: true}, nil
}

// scanValue reads a TOML value starting at lines[kl][from:], tracking strings
// and bracket depth so a multi-line array is one span, and splits off a
// trailing `# comment` on the final line. Returns the value text (joined with
// newlines), the comment ("" when none), and the index of the span's last line.
func scanValue(lines []string, kl, from, end int) (string, string, int, error) {
	depth := 0
	var parts []string
	for i := kl; i < end; i++ {
		s := lines[i]
		start := 0
		if i == kl {
			start = from
		}
		j := start
		inBasic, inLiteral := false, false
		commentAt := -1
		for j < len(s) {
			c := s[j]
			switch {
			case inBasic:
				switch c {
				case '\\':
					j++ // skip the escaped char
				case '"':
					inBasic = false
				}
			case inLiteral:
				if c == '\'' {
					inLiteral = false
				}
			case c == '"':
				inBasic = true
			case c == '\'':
				inLiteral = true
			case c == '[' || c == '{':
				depth++
			case c == ']' || c == '}':
				depth--
			case c == '#':
				commentAt = j
			}
			if commentAt >= 0 {
				break
			}
			j++
		}
		if commentAt >= 0 {
			parts = append(parts, s[start:commentAt])
			if depth <= 0 {
				return strings.Join(parts, "\n"), strings.TrimSpace(s[commentAt:]), i, nil
			}
			// A '#' inside an unclosed array is still a comment to EOL in TOML;
			// keep scanning on the next line.
			continue
		}
		parts = append(parts, s[start:])
		if depth <= 0 {
			return strings.Join(parts, "\n"), "", i, nil
		}
	}
	return "", "", 0, fmt.Errorf("unterminated value for a key starting at line %d", kl+1)
}

// ParseTOMLValueText decodes a single TOML value's text (what scanValue
// extracted) into a Go value, for the envelope's `before`. Best-effort: text
// that no longer parses (half-merged files) reports as the raw string.
func ParseTOMLValueText(text string) any {
	var m map[string]any
	if err := toml.Unmarshal([]byte("x = "+text), &m); err == nil {
		return m["x"]
	}
	return text
}
