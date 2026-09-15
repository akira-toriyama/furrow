package cli

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/schema"
	"github.com/akira-toriyama/furrow/internal/version"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the furrow version (with build commit/date when stamped)",
		Example: "  furrow version\n" +
			"  furrow version --json   # {version, commit, date, modified} for scripts/agents",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			info := version.Resolve()
			if jsonMode() {
				// Info carries json tags; the full commit sha stays here (the
				// human string shortens it) so an agent can match exactly.
				emitObject(info)
				return nil
			}
			fmt.Fprintln(out, info.String())
			return nil
		},
	}
}

func newSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema [task|meta|repo|epic]",
		Short: "Print the JSON Schema for a task shard, meta.json, a repo review shard, or an epic shard",
		Long: "Print the JSON Schema (draft 2020-12) for the store's files. With no\n" +
			"argument (or \"task\") it prints the schema for one .furrow/tasks/<id>.json\n" +
			"shard; \"meta\" prints the schema for .furrow/meta.json; \"repo\" prints the\n" +
			"schema for one .furrow/repos/<owner>__<repo>.json review shard; \"epic\" the\n" +
			"one for .furrow/epics/<id>.json. These are the single source of truth;\n" +
			"docs/schema/furrow.{task.v2,meta.v2,repo.v1,epic.v2}.json are committed\n" +
			"copies and CI diffs them so they cannot drift. The output is already\n" +
			"JSON: --json prints the same bytes, --ndjson compacts them to one line.",
		Args:      cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"task", "meta", "repo", "epic"},
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := "task"
			if len(args) == 1 {
				kind = args[0]
			}
			// schema consts are already valid JSON text; print verbatim (not via
			// the JSON encoder) so the bytes match the committed file exactly —
			// under --ndjson, compacted to the one line that mode promises.
			var doc string
			switch kind {
			case "task":
				doc = schema.TaskV2
			case "meta":
				doc = schema.MetaV2
			case "repo":
				doc = schema.RepoV1
			case "epic":
				doc = schema.EpicV2
			default:
				return core.Validationf("", "unknown schema kind %q (want \"task\", \"meta\", \"repo\", or \"epic\")", kind)
			}
			if flagNDJSON {
				var b bytes.Buffer
				if err := json.Compact(&b, []byte(doc)); err != nil {
					return core.Internalf("", "schema is not valid JSON: %v", err)
				}
				fmt.Fprintln(out, b.String())
				return nil
			}
			fmt.Fprint(out, doc)
			return nil
		},
	}
}
