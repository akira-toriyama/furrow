package cli

import (
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// newParentCmd wires `furrow parent` — the hierarchy edge's own command, shaped
// like `dep`: a mutation by default, a both-directions READ with --list.
//
// Until now `parent` was the one field with no command. It could be set at `add
// --parent` and never changed, so fixing a mis-filed task meant hand-editing a
// machine-written shard — the one thing CLAUDE.md tells you never to do.
func newParentCmd() *cobra.Command {
	var rm, list bool
	cmd := &cobra.Command{
		Use:   "parent <id> [<parent-id>]",
		Short: "Set, clear (--rm), or list (--list) a task's parent",
		Long: "Put <id> under <parent-id> in the hierarchy. With --rm, detach it instead\n" +
			"(the task becomes top-level). Re-parenting is acyclic: the parent must exist,\n" +
			"a task cannot be its own parent, and an edge that would close a loop is a\n" +
			"validation error (a cycle has no root, so every task in it would belong to no\n" +
			"tree at all). A parent already in the done lane is allowed — re-filing a\n" +
			"leftover under the epic it came from is legitimate — and `furrow lint` warns\n" +
			"parent-done on an open task left under a closed one.\n\n" +
			"With --list, don't mutate — read <id>'s hierarchy neighborhood in BOTH\n" +
			"directions: the parent it hangs under (null when top-level) and the children\n" +
			"hanging under it, each resolved to id+title+lane. --json/--ndjson emit one\n" +
			"object with both. The reverse edge is the point: \"what is still under this\n" +
			"epic?\" is a command, not a full-board dump.",
		Example: "  furrow parent t-k3m9p t-a1b2c        # file it under the epic\n" +
			"  furrow parent t-k3m9p --rm           # detach: back to top-level\n" +
			"  furrow parent t-a1b2c --list --json  # its parent and everything under it",
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("list") || cmd.Flags().Changed("rm") {
				return cobra.ExactArgs(1)(cmd, args)
			}
			return cobra.ExactArgs(2)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if list && rm {
				return core.Validationf(args[0], "--list reads, --rm writes; use one or the other")
			}
			a, err := openApp()
			if err != nil {
				return err
			}
			if list {
				res, err := a.ParentList(args[0])
				if err != nil {
					return err
				}
				return emitParentList(res)
			}
			id, parent, verb := args[0], "", "parent-"
			if !rm {
				parent, verb = args[1], "parent"
			}
			return emitMutation(a, verb, id, func() (*core.Task, error) {
				return a.Reparent(id, parent)
			})
		},
	}
	cmd.Flags().BoolVar(&rm, "rm", false, "detach the parent (the task becomes top-level)")
	cmd.Flags().BoolVar(&list, "list", false, "read-only: show the parent and the children, both directions")
	return cmd
}
