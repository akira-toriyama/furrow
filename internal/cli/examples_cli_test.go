package cli

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Every Example line of every command parses against the tree it documents:
// the command resolves, its flags parse, its arity holds, every id in it has
// the board's real shape (prefix + 5 chars — a 4-char box id resolves only as
// a prefix), and no example carries a literal `\n`. The lines are shell: a
// `# comment` tail is dropped, `&&`/`|` split them, redirections are skipped.
func TestEveryExampleParses(t *testing.T) {
	initStore(t)
	idShape := regexp.MustCompile(`^[te]-[a-z0-9]+$`)
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		for _, line := range strings.Split(c.Example, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.Contains(line, `\n`) {
				t.Errorf("%s: example carries a literal \\n: %s", path, line)
			}
			if i := strings.Index(line, " #"); i >= 0 {
				line = line[:i]
			}
			for _, seg := range regexp.MustCompile(`\s*(&&|\|\||\|)\s*`).Split(line, -1) {
				words := shellWords(seg)
				if len(words) == 0 || words[0] != "furrow" {
					continue
				}
				args := dropRedirections(words[1:])
				// A fresh tree per example: flag state (Changed) would otherwise
				// leak from one example into the next one's Args hook.
				fresh := newRootCmd()
				cmd, rest, err := fresh.Find(args)
				if err != nil || cmd == fresh {
					t.Errorf("%s: example names no command: %s (%v)", path, seg, err)
					continue
				}
				if err := cmd.ParseFlags(rest); err != nil {
					t.Errorf("%s: flags do not parse: %s (%v)", path, seg, err)
					continue
				}
				positional := cmd.Flags().Args()
				if cmd.Args != nil {
					if err := cmd.Args(cmd, positional); err != nil {
						t.Errorf("%s: arity: %s (%v)", path, seg, err)
					}
				}
				for _, w := range args {
					if idShape.MatchString(w) && len(w) != 7 {
						t.Errorf("%s: id %q is not the board's shape (prefix + 5 chars): %s", path, w, seg)
					}
				}
			}
		}
		for _, sub := range c.Commands() {
			walk(sub, strings.TrimSpace(path+" "+sub.Name()))
		}
	}
	walk(newRootCmd(), "furrow")
}

// shellWords splits one shell segment on whitespace, honouring single and
// double quotes and $'…' (enough for help examples; no expansion).
func shellWords(s string) []string {
	var words []string
	var b strings.Builder
	in := false
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			in = true
		case r == ' ' || r == '\t':
			if in {
				words = append(words, b.String())
				b.Reset()
				in = false
			}
		default:
			b.WriteRune(r)
			in = true
		}
	}
	if in {
		words = append(words, b.String())
	}
	return words
}

// dropRedirections removes `< file`, `> file`, `2>/dev/null` style tokens.
func dropRedirections(args []string) []string {
	var out []string
	skip := false
	for _, a := range args {
		if skip {
			skip = false
			continue
		}
		if a == "<" || a == ">" || a == ">>" || a == "2>" {
			skip = true
			continue
		}
		if strings.HasPrefix(a, "<") || strings.HasPrefix(a, ">") || strings.HasPrefix(a, "2>") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// The wording drifts the 2026-10 survey named, each pinned to the help it
// lives in.
func TestHelpWordingDrifts(t *testing.T) {
	initStore(t)
	cases := []struct {
		args    []string
		want    []string
		forbids []string
	}{
		{[]string{"stats", "--help"}, nil, []string{"closed −", "budget"}},
		{[]string{"ls", "--help"}, []string{"descendant-of:/ancestor-of:"}, nil},
		{[]string{"epic", "--help"}, []string{"deactivate", "reopen", "dep", "rm"}, nil},
		{[]string{"move", "--help"}, []string{"leaving it (a reopen) clears it"}, nil},
		{[]string{"next", "--help"}, []string{"REPLACES which lanes qualify"}, []string{"widens"}},
		{[]string{"doctor", "--help"}, []string{"session-registry-unreadable"}, nil},
		{[]string{"brief", "--help"}, []string{"brief never touches git"}, []string{"sync → next"}},
		{[]string{"edit", "--help"}, []string{"--body-file plan.md"}, []string{`\n`}},
		{[]string{"rm", "--help"}, []string{"exit 2 while referenced"}, nil},
		{[]string{"add", "--help"}, []string{"dependency task id (repeatable; comma-separated)"}, nil},
		{[]string{"archive", "--help"}, []string{"apply the previewed write (without --yes the command only previews and writes nothing)"}, nil},
		{[]string{"done", "--help"}, []string{"apply the previewed write (without --yes the command only previews and writes nothing)"}, nil},
	}
	for _, c := range cases {
		help := mustRun(t, c.args...)
		for _, w := range c.want {
			if !strings.Contains(help, w) {
				t.Errorf("%v lacks %q", c.args, w)
			}
		}
		for _, f := range c.forbids {
			if strings.Contains(help, f) {
				t.Errorf("%v still says %q", c.args, f)
			}
		}
	}
}
