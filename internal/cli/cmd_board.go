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
			if jsonMode() {
				emitObject(info)
				return nil
			}
			printBoardHuman(info)
			return nil
		},
	}
}

// newBoardsCmd lists every CONFIGURED board — the machine-wide view `board`
// cannot give, because `board` answers for one cwd and exits 2 where no board
// is in scope. `boards` never resolves against cwd at all, so it answers
// everywhere; a GUI front-end running at cwd=/ reads it to locate the central
// board instead of re-parsing furrow's config format itself.
func newBoardsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "boards",
		Short: "List the configured boards (user-level config), independent of cwd",
		Long: "List every [[board]] in the user-level config\n" +
			"(${XDG_CONFIG_HOME:-~/.config}/furrow/config.toml), in file order, WITHOUT\n" +
			"resolving against cwd — this works (exit 0) exactly where other commands\n" +
			"exit 2 because no board is in scope, so it is the diagnosis call for \"which\n" +
			"boards does this machine know about?\" and the one-call bootstrap for a GUI\n" +
			"front-end. The JSON is {config, boards: []}: `config` names the file read\n" +
			"(whether or not it exists), and each entry carries the resolved store path\n" +
			"and scopes, the DECLARED repo/label (\"auto\" cannot resolve without a\n" +
			"checkout), an `exists` flag, and the same vocabulary + schema keys as\n" +
			"`furrow board` (one parser reads both). A missing board keeps an EMPTY\n" +
			"vocabulary — reported, never guessed. No config or no usable [[board]] is\n" +
			"boards: [] with exit 0 — that emptiness is the finding. The FURROW_BOARD env\n" +
			"override is a per-invocation redirect, not machine config, so it is not\n" +
			"listed.",
		Example: "  furrow boards           # human summary of every configured board\n" +
			"  furrow boards --json    # {config, boards: [{store, scopes, repo, exists, lanes, writable, ...}]}\n" +
			"  furrow boards --json | jq -r '.boards[].store'",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			list, warns, err := app.Boards()
			if err != nil {
				return err
			}
			for _, w := range warns {
				fmt.Fprintf(errOut, "note: %s\n", w)
			}
			if jsonMode() {
				emitObject(list)
				return nil
			}
			printBoardsHuman(list)
			return nil
		},
	}
}

// printBoardsHuman renders the configured-board listing: the config path, then
// one small block per board (details live in --json).
func printBoardsHuman(l *app.BoardsList) {
	fmt.Fprintf(out, "config: %s\n", l.Config)
	if len(l.Boards) == 0 {
		fmt.Fprintln(out, "no boards configured — `furrow config init` writes the template")
		return
	}
	for _, b := range l.Boards {
		state := b.SchemaState
		if !b.Exists {
			state = "missing (not on disk)"
		}
		repo := b.Repo
		if repo == "" {
			repo = "(none)"
		}
		fmt.Fprintf(out, "\n%s\n", b.Store)
		fmt.Fprintf(out, "  repo: %s  state: %s  writable: %t\n", repo, state, b.Writable)
		if b.Label != "" {
			fmt.Fprintf(out, "  add tag: %s\n", b.Label)
		}
		fmt.Fprintf(out, "  scopes: %s\n", strings.Join(b.Scopes, ", "))
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
	if b.AutoCommit {
		fmt.Fprintf(out, "autocommit: on (commits .furrow/ after each mutating command)\n")
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
