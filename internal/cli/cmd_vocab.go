package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/v9/internal/app"
	"github.com/akira-toriyama/furrow/v9/internal/core"
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
// Visible, unlike `commands`: README's error-kind and lint bullets and the
// refusals' remedies send the reader to it, so it is CLI contract and sits in
// the command table (it was hidden while the docs pointed at it).
func newVocabCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "vocab [name]",
		Short: "Print a closed vocabulary (error kinds, lint codes, …), one member per line",
		Args:  cobra.MaximumNArgs(1),
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
						Kind:       core.KindValidation,
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

// emitLines prints one string per line: a JSON array under --json, one JSON
// string per line under --ndjson (one value per line, as every list read
// streams), plain text otherwise.
func emitLines(ss []string) {
	switch {
	case flagNDJSON:
		for _, s := range ss {
			fmt.Fprintln(out, string(mustJSON(s)))
		}
	case jsonMode():
		emitObject(ss)
	default:
		for _, s := range ss {
			fmt.Fprintln(out, s)
		}
	}
}

// topLevelCommandNames walks root's registered subcommands — the same set the
// generated README table and the docs claims call "the commands": visible only
// (the hidden `commands` is the README generator's own), without
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
