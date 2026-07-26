package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// newVocabCmd is the hidden emitter behind scripts/check-docs-vocab.sh — the
// prose-vs-code vocabulary drift guard. `furrow vocab` lists the vocabulary
// names; `furrow vocab <name>` prints that vocabulary's members one per line,
// straight from the registry that owns it (app.Vocabularies, plus the cobra
// tree for `commands`), so a documented enumeration can be diffed against the
// code without a second hand-kept list. Sibling of `furrow commands` (the
// README table generator): both exist because every hand-kept copy of a closed
// vocabulary eventually loses members — the 2026-07 audit found five.
//
// Hidden: repo tooling like `commands`, not part of the CLI contract.
func newVocabCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "vocab [name]",
		Short:  "Print a closed vocabulary, one member per line (docs drift-guard source)",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			vocabs := app.Vocabularies()
			names := make([]string, 0, len(vocabs)+1)
			for n := range vocabs {
				names = append(names, n)
			}
			names = append(names, "commands")
			sort.Strings(names)

			if len(args) == 0 {
				emitLines(names)
				return nil
			}
			name := args[0]
			var members []string
			switch name {
			case "commands":
				members = topLevelCommandNames(cmd.Root())
			default:
				m, ok := vocabs[name]
				if !ok {
					return &core.Error{
						Code:       core.CodeValidation,
						Msg:        fmt.Sprintf("unknown vocabulary %q (known: %s)", name, strings.Join(names, ", ")),
						Candidates: names,
					}
				}
				members = m
			}
			emitLines(members)
			return nil
		},
	}
}

// emitLines prints one string per line, or a JSON array under --json/--ndjson.
func emitLines(ss []string) {
	if jsonMode() {
		emitObject(ss)
		return
	}
	for _, s := range ss {
		fmt.Fprintln(out, s)
	}
}

// topLevelCommandNames walks root's registered subcommands — the same set the
// generated README table and the docs claims call "the commands": visible only
// (hidden repo tooling like `commands`/`vocab` is not CLI contract), without
// cobra's help/completion furniture, in registration order. A namespace parent
// like `config` counts once.
func topLevelCommandNames(root *cobra.Command) []string {
	var names []string
	for _, c := range root.Commands() {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		names = append(names, c.Name())
	}
	return names
}
