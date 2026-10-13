package cli

import (
	"github.com/spf13/cobra"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// depVerb reads an optional leading `add` / `rm` token off a dep command's
// arguments: `dep add <id> <dep-id>` and `dep rm <id> <dep-id>` are the
// spellings a first-time agent reaches for (t-5r9g), and they mean exactly the
// bare `dep <id> <dep-id>` and `dep <id> <dep-id> --rm`. Reading the token as a
// verb loses nothing: an id always carries its prefix and a dash, so neither
// word can name a task or a box.
func depVerb(args []string) (verb string, rest []string) {
	if len(args) > 0 && (args[0] == "add" || args[0] == "rm") {
		return args[0], args[1:]
	}
	return "", args
}

// depVerbArgs is the cobra Args check shared by `dep` and `epic dep`: the
// verb-first spelling must still carry the two ids, and a verb never combines
// with --list (a read) or contradicts --rm.
func depVerbArgs(cmd *cobra.Command, args []string) error {
	verb, rest := depVerb(args)
	path := cmd.CommandPath()
	if cmd.Flags().Changed("list") {
		if verb != "" {
			return core.Validationf("", "`%s %s` writes and --list reads; drop one of them", path, verb)
		}
		return cobra.ExactArgs(1)(cmd, rest)
	}
	if verb == "add" && cmd.Flags().Changed("rm") {
		return core.Validationf("", "`%s add` and --rm contradict; use `%s rm <id> <dep-id>` or `%s <id> <dep-id> --rm` to remove", path, path, path)
	}
	return cobra.MinimumNArgs(2)(cmd, rest)
}
