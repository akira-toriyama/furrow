package cli

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/spf13/cobra"
)

// newBoardCmd prints the resolved board an agent is about to act on: the store
// path (where writes land), how it was discovered, the repo scope filtering
// reads, and the lane vocabulary. It is the introspection call that answers
// "what lanes exist and what scope is active" without having to provoke an
// error (the old only way to learn the lanes was to fail a `move` on purpose).
func newBoardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "board",
		Short: "Print the active board: store path, scope, lane vocabulary, and schema state",
		Long: "Print the resolved board furrow is acting on: the store path (where writes\n" +
			"land), how it was discovered (env | local | pointer | user-config), the repo\n" +
			"scope that filters reads, and the lane vocabulary (lanes / next-lanes /\n" +
			"default / done / terminal) plus the stale/archive windows. It is the\n" +
			"introspection call for \"what lanes exist and what scope is active\" — so a\n" +
			"typo'd `-s`/`move` need not be provoked to learn the lanes. --json emits the\n" +
			"object; --ndjson emits it as one compact line.\n\n" +
			"It also answers \"can I write here, and if not, which side is stale?\" without\n" +
			"provoking an error:\n" +
			"  schema_version         the layout the BOARD declares (0 = unstamped)\n" +
			"  binary_schema_version  the layout THIS furrow writes\n" +
			"  schema_state           current | outdated | too-new | unreadable\n" +
			"  writable               true only when the two agree\n" +
			"Both stores are covered — a board that has archived anything is TWO stores\n" +
			"(.furrow/ and .furrow/archive/), each with its own layout version; the worst\n" +
			"state wins. This command NEVER fails on a version mismatch — it reports it, so\n" +
			"it is the one thing that still answers when nothing else can. Read it as a\n" +
			"pre-flight (`writable != true` -> stop) instead of watching every task read\n" +
			"fail with \"task not found\".",
		Example: "  furrow board            # human summary\n" +
			"  furrow board --json     # {store, source, scope_repo, lanes, schema_state, writable, ...}\n" +
			"  furrow board --json | jq -e '.writable'   # CI pre-flight: is this board writable?",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			info := a.Board()
			switch {
			case flagNDJSON:
				printNDJSONValue(info)
			case flagJSON:
				printJSON(info)
			default:
				printBoardHuman(info)
			}
			return nil
		},
	}
}

// printBoardHuman renders the board snapshot as an aligned key/value block on
// stdout (JSON/NDJSON are handled by the caller).
func printBoardHuman(b app.BoardInfo) {
	scope := b.ScopeRepo
	if scope == "" {
		scope = "(whole board)"
	}
	fmt.Fprintf(out, "store:    %s\n", b.Store)
	fmt.Fprintf(out, "source:   %s\n", b.Source)
	fmt.Fprintf(out, "scope:    %s (auto_filter=%t)\n", scope, b.AutoFilter)
	if b.DefaultLabel != "" {
		fmt.Fprintf(out, "add tag:  %s\n", b.DefaultLabel)
	}
	fmt.Fprintf(out, "lanes:    %s\n", strings.Join(b.Lanes, ", "))
	fmt.Fprintf(out, "next:     %s\n", strings.Join(b.NextLanes, ", "))
	fmt.Fprintf(out, "default:  %s\n", b.DefaultLane)
	fmt.Fprintf(out, "done:     %s\n", b.DoneLane)
	fmt.Fprintf(out, "terminal: %s\n", strings.Join(b.Terminal, ", "))
	containers := strings.Join(b.Containers, ", ")
	if containers == "" {
		containers = "(none)"
	}
	fmt.Fprintf(out, "types:    %s (default: %s, containers: %s)\n", strings.Join(b.Types, ", "), b.DefaultType, containers)
	fmt.Fprintf(out, "schema:   %s\n", schemaLine(b))
	fmt.Fprintf(out, "stale_days: %d, archive_older_than_days: %d, labels_required: %t\n",
		b.StaleDays, b.ArchiveOlderThanDays, b.LabelsRequired)
}

// schemaLine says which side is stale and what to do about it. `board` is the
// one command that still answers on a board no other command can open, so this
// line is the human's first diagnosis when furrow starts refusing writes.
func schemaLine(b app.BoardInfo) string {
	switch b.SchemaState {
	case app.SchemaOutdated:
		return fmt.Sprintf("v%d (board) / v%d (binary) — READ-ONLY: run `furrow upgrade`",
			b.SchemaVersion, b.BinarySchemaVersion)
	case app.SchemaTooNew:
		// Not "read-only" — this binary cannot read it either; every command but
		// this one exits 3.
		return fmt.Sprintf("v%d (board) / v%d (binary) — UNREADABLE: this furrow is too old; update it",
			b.SchemaVersion, b.BinarySchemaVersion)
	case app.SchemaUnreadable:
		return fmt.Sprintf("unreadable meta.json / v%d (binary) — restore it from git", b.BinarySchemaVersion)
	default:
		return fmt.Sprintf("v%d (board) / v%d (binary) — writable", b.SchemaVersion, b.BinarySchemaVersion)
	}
}
