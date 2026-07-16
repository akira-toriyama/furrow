package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newCommandsCmd is the hidden generator behind the READMEs' command table.
// It renders every visible command as one Markdown table row straight from
// the cobra tree — Use, Short, Aliases, and the local flag set are the single
// source of truth, so the table cannot say something the binary does not.
// scripts/gen-command-table.sh splices this output between the READMEs'
// `<!-- commands:begin/end -->` markers, and check.sh + CI diff the spliced
// block against a fresh run (the docs audit found search/stats/parent/review
// missing from every hand-kept list — this is the fix that stays fixed).
//
// Hidden: it is repo tooling like `completion`, not part of the CLI contract
// (the canonical command list in CLAUDE.md deliberately excludes it).
func newCommandsCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "commands",
		Short:  "Print the command table as Markdown (README generator)",
		Hidden: true,
		Args:   cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			fmt.Fprintln(w, "| Command | What it does | Flags |")
			fmt.Fprintln(w, "|---|---|---|")
			root := cmd.Root()
			for _, c := range root.Commands() {
				writeCommandRows(w, root, c)
			}
		},
	}
}

// writeCommandRows emits the table row for a leaf command, recursing into
// visible subcommands (a parent like `config` is a namespace, not a row —
// mirroring how the hand-written table always listed `config init`/`config
// path`). help/completion are cobra furniture, not furrow's contract.
func writeCommandRows(w io.Writer, root, c *cobra.Command) {
	if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
		return
	}
	if c.HasAvailableSubCommands() {
		for _, sub := range c.Commands() {
			writeCommandRows(w, root, sub)
		}
		return
	}
	fmt.Fprintf(w, "| %s | %s | %s |\n", commandCell(root, c), mdCell(c.Short), flagsCell(c))
}

// commandCell renders `config init` / `add <title>...` — the full command path
// with the Use line's argument tail, plus any aliases.
func commandCell(root, c *cobra.Command) string {
	path := strings.TrimPrefix(c.CommandPath(), root.Name()+" ")
	tail := strings.TrimPrefix(c.Use, c.Name())
	cell := "`" + path + tail + "`"
	if len(c.Aliases) > 0 {
		cell += " (alias `" + strings.Join(c.Aliases, "`, `") + "`)"
	}
	return mdCell(cell)
}

// flagsCell lists the command's own flags (inherited globals like --json are
// documented once, not per row), shorthand first, in pflag's sorted order.
func flagsCell(c *cobra.Command) string {
	var parts []string
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" || f.Hidden {
			return
		}
		if f.Shorthand != "" {
			parts = append(parts, "`-"+f.Shorthand+"/--"+f.Name+"`")
		} else {
			parts = append(parts, "`--"+f.Name+"`")
		}
	})
	if len(parts) == 0 {
		return "—"
	}
	return mdCell(strings.Join(parts, ", "))
}

// mdCell escapes the one character that breaks a Markdown table cell.
func mdCell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
