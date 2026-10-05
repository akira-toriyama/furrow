package cli

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// bodyFlags is the --body / --body-file pair `add`, `edit` and `epic add`
// share: --body carries the markdown ITSELF, --body-file names a file holding
// it, and either spelled "-" reads stdin (readTextArg's convention). Every
// command resolves the pair through text, so the two can never disagree about
// what a path-shaped value means.
type bodyFlags struct {
	body string
	file string
}

func (b *bodyFlags) register(cmd *cobra.Command, bodyUsage string) {
	cmd.Flags().StringVar(&b.body, "body", "", bodyUsage)
	cmd.Flags().StringVar(&b.file, "body-file", "", "read the body markdown from this file ('-' reads stdin); --body takes the markdown itself, never a path")
	cmd.MarkFlagsMutuallyExclusive("body", "body-file")
}

func (b *bodyFlags) given(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file")
}

// stdinFlag names the flag that will consume stdin, "" when neither does — a
// caller with another stdin reader (`add --stdin`, `--batch -`) refuses the
// combination before text reads anything.
func (b *bodyFlags) stdinFlag() string {
	switch {
	case b.body == "-":
		return "--body"
	case b.file == "-":
		return "--body-file"
	}
	return ""
}

// text resolves the pair to the body markdown ("" when neither flag was given).
// stdinFree is false when another reader owns stdin, so the refusal below never
// offers a stdin spelling that the same call would reject.
//
// A one-line --body naming an existing path is refused rather than stored: the
// caller meant the file's contents, and exit 0 with the path as the whole body
// is the confident wrong answer (t-4x3c — a recurring agent mistake). The
// escape for a body that really is that one line is stdin, which is never
// path-checked.
func (b *bodyFlags) text(cmd *cobra.Command, stdinFree bool) (string, error) {
	if b.stdinFlag() != "" {
		return readTextArg(cmd, "-")
	}
	if cmd.Flags().Changed("body-file") {
		return readBodyFile(b.file)
	}
	if !namesFile(b.body) {
		return b.body, nil
	}
	word := shellQuote(b.body)
	msg := fmt.Sprintf("--body takes the markdown itself, and %q is an existing file — pass --body-file to read it", b.body)
	candidates := []string{"--body-file " + word}
	if stdinFree {
		msg += " (or pipe it to --body -); to store that one line as the body, pipe the line to --body -"
		candidates = append(candidates, "--body - < "+word)
	}
	e := core.Validationf("", "%s", msg)
	e.Candidates = candidates
	return "", e
}

// readBodyFile reads the file --body-file names. A file with no text is exit 2
// like an unreadable one: the caller named a body, and seeding the default
// heading instead would hide that it never arrived.
func readBodyFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", core.Validationf("", "--body-file was given an empty value; pass a path, or drop the flag")
	}
	data, err := os.ReadFile(path) // #nosec G304 -- the path is the caller's own --body-file argument, read once and never written
	if err != nil {
		return "", core.Validationf("", "--body-file: %v", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", core.Validationf("", "--body-file: %q is empty; a file named as the body must carry one", path)
	}
	return string(data), nil
}

// namesFile reports whether s is a single line that resolves to something
// readable as a file — a regular file, but also the /dev/fd/N a `<(cmd)`
// substitution leaves behind. A directory is not a body anyone could have
// meant to read, and a stat failure of any kind (absent, unreadable, too long
// to be a path) leaves s the literal markdown it was passed as.
func namesFile(s string) bool {
	if s == "" || strings.Contains(s, "\n") {
		return false
	}
	fi, err := os.Stat(s)
	return err == nil && !fi.IsDir()
}

// shellQuote renders s as one POSIX shell word, so a candidate still names the
// same path after it is pasted (a space, a `$`, a leading `~`).
func shellQuote(s string) string {
	plain := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_@%+=:,./-", r)
	}
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !plain(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
