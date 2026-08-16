package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/spf13/cobra"
)

// The shared filtering-read flags have ONE registrar, like -q (addQueryFlag)
// and --expect-updated before them (t-jbph): -l/-r/-e/-n/--archived/--since/
// --until were hand-registered per command — six copies of -l's usage line,
// three spellings of -r's, four of -n's — and the wordings had already
// drifted apart in ways `--help` showed. A command asks for exactly the flags
// it carries; the canonical usage lines live here and nowhere else.

// filterFlags bundles the shared flag values for one command. Zero-valued
// defaults apply except where a command pre-sets one before addFilterFlags
// (brief sets limit=3 — the registrar takes the field's current value as the
// flag default).
type filterFlags struct {
	label    []string
	repo     string
	epic     string
	limit    int
	archived bool
	since    string
	until    string
}

// filterFlag selects one shared flag, optionally overriding the canonical
// usage line. An override is for a command where the flag's MEANING differs
// (next's -e reads one box explicitly instead of filtering members; brief's
// -n caps body-attached picks, not rows) — a preferred spelling of the same
// meaning is not a reason, that is exactly the drift this registrar removes.
type filterFlag struct {
	name  string
	usage string
}

// want selects a flag with its canonical usage.
func want(name string) filterFlag { return filterFlag{name: name} }

// wantUsage selects a flag with a command-specific usage — leave a comment at
// the call site saying why the meaning differs.
func wantUsage(name, usage string) filterFlag { return filterFlag{name: name, usage: usage} }

// The canonical usage lines — the best wording of what were 2-4 divergent
// spellings each.
const (
	usageLabel    = "filter by label (OR; comma-separated or repeated -l, e.g. -l bug,urgent or -l bug -l urgent); a pure tag that ANDs with the board scope"
	usageRepo     = "filter by repo (owner/repo or a unique short name; '' = whole board)"
	usageEpic     = "only that epic's members (id, unique id prefix, or unique title substring; strict — no unfiled carve-out)"
	usageLimit    = "max rows (0 = all)"
	usageArchived = "read from the archive store (.furrow/archive/) instead of the hot board"
	usageSince    = "only tasks updated on/after this date (YYYY-MM-DD or RFC3339)"
	usageUntil    = "only tasks updated on/before this date (YYYY-MM-DD includes the whole day, or RFC3339)"
)

// addFilterFlags registers the selected shared flags on cmd, storing values in
// f. An unknown name is a programmer error (panic at wiring time, caught by
// any test that builds the command tree).
func addFilterFlags(cmd *cobra.Command, f *filterFlags, flags ...filterFlag) {
	for _, fl := range flags {
		usage := fl.usage
		use := func(canonical string) string {
			if usage != "" {
				return usage
			}
			return canonical
		}
		switch fl.name {
		case "label":
			cmd.Flags().StringArrayVarP(&f.label, "label", "l", nil, use(usageLabel))
		case "repo":
			cmd.Flags().StringVarP(&f.repo, "repo", "r", "", use(usageRepo))
		case "epic":
			cmd.Flags().StringVarP(&f.epic, "epic", "e", "", use(usageEpic))
		case "limit":
			cmd.Flags().IntVarP(&f.limit, "limit", "n", f.limit, use(usageLimit))
		case "archived":
			cmd.Flags().BoolVar(&f.archived, "archived", false, use(usageArchived))
		case "since":
			cmd.Flags().StringVar(&f.since, "since", "", use(usageSince))
		case "until":
			cmd.Flags().StringVar(&f.until, "until", "", use(usageUntil))
		default:
			panic(fmt.Sprintf("addFilterFlags: unknown filter flag %q", fl.name))
		}
	}
}

// window applies --since/--until to o — the ONE parse of the pair (ls and
// stats used to carry 14-line copies of it). A bare --until includes the
// whole day; parse errors are exit 2.
func (f *filterFlags) window(cmd *cobra.Command, o *app.QueryOpts) error {
	if cmd.Flags().Changed("since") {
		ts, err := parseDateBound(f.since, false)
		if err != nil {
			return err
		}
		o.Since = &ts
	}
	if cmd.Flags().Changed("until") {
		ts, err := parseDateBound(f.until, true)
		if err != nil {
			return err
		}
		o.Until = &ts
	}
	return nil
}
