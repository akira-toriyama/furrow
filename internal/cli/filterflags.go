package cli

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/furrow/v9/internal/app"
	"github.com/akira-toriyama/furrow/v9/internal/core"
	"github.com/spf13/cobra"
)

// The shared filtering-read flags have ONE registrar, like -q (addQueryFlag)
// and --expect-updated before them (t-jbph): -l/-r/-e/-n/--archived/--since/
// --until were hand-registered per command — six copies of -l's usage line,
// three spellings of -r's, four of -n's — and the wordings had already
// drifted apart in ways `--help` showed (-s joined later: three byte-identical
// copies outside the registrar, t-m31c). A command asks for exactly the flags
// it carries; the canonical usage lines live here and nowhere else — the
// write selector included, whose -l/-r mean "select" rather than "filter" and
// so override the wording, never the registration.

// filterFlags bundles the shared flag values for one command. Zero-valued
// defaults apply except where a command pre-sets one before addFilterFlags
// (brief sets limit=3 — the registrar takes the field's current value as the
// flag default).
type filterFlags struct {
	status   []string
	label    []string
	repo     []string // at most one value — see singleFlag
	epic     []string // at most one value — see singleFlag
	limit    int
	archived bool
	since    string
	until    string
}

// repoValue / epicValue are the one value -r / -e may carry ("" when absent).
func (f *filterFlags) repoValue() string { return firstOf(f.repo) }
func (f *filterFlags) epicValue() string { return firstOf(f.epic) }

func firstOf(vs []string) string {
	if len(vs) == 0 {
		return ""
	}
	return vs[0]
}

// singleFlag refuses a flag given twice. -r and -e were plain string flags, so
// a second occurrence silently replaced the first: `ls -r o/a -r o/b` listed
// o/b alone, and a write selector `-r a -r b` previewed — and with --yes
// applied — a narrower set than the operator had asked for. One repo and one
// box is what a read scopes to; an OR over several is -q's job, and the
// refusal names that spelling.
func singleFlag(cmd *cobra.Command, name, short string, vals []string, qualifier string) error {
	if len(vals) <= 1 {
		return nil
	}
	return core.Validationf("", "-%s given %d times — a read scopes to one %s; for an OR over several use -q '%s:%s'", short, len(vals), name, qualifier, strings.Join(vals, ","))
}

// joinQuery ANDs repeated -q values: the grammar ANDs terms by juxtaposition,
// so the queries are joined with a space. A lone `-q ”` stays the deliberate
// match-everything query. (A second -q used to replace the first in silence,
// which widened a write selector's target set.)
func joinQuery(qs []string) string {
	parts := make([]string, 0, len(qs))
	for _, q := range qs {
		if strings.TrimSpace(q) != "" {
			parts = append(parts, q)
		}
	}
	return strings.Join(parts, " ")
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
	usageStatus   = "filter by lane (OR; comma-separated or repeated -s, e.g. -s inbox,backlog or -s inbox -s backlog)"
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
		case "status":
			cmd.Flags().StringArrayVarP(&f.status, "status", "s", nil, use(usageStatus))
		case "label":
			cmd.Flags().StringArrayVarP(&f.label, "label", "l", nil, use(usageLabel))
		case "repo":
			cmd.Flags().StringArrayVarP(&f.repo, "repo", "r", nil, use(usageRepo))
			guardSingle(cmd, "repo", "r", &f.repo, "repo")
		case "epic":
			cmd.Flags().StringArrayVarP(&f.epic, "epic", "e", nil, use(usageEpic))
			guardSingle(cmd, "epic", "e", &f.epic, "epic")
		case "limit":
			cmd.Flags().IntVarP(&f.limit, "limit", "n", f.limit, use(usageLimit))
			// A negative cap read as "all" (every consumer treats <= 0 as
			// uncapped); say so instead of honouring a typo (t-jty7).
			prev := cmd.PreRunE
			cmd.PreRunE = func(c *cobra.Command, args []string) error {
				if prev != nil {
					if err := prev(c, args); err != nil {
						return err
					}
				}
				if f.limit < 0 {
					return core.Validationf("", "-n %d: a row cap cannot be negative (0 = all)", f.limit)
				}
				return nil
			}
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

// guardSingle chains singleFlag into cmd's PreRunE (the -n guard's shape).
func guardSingle(cmd *cobra.Command, name, short string, vals *[]string, qualifier string) {
	prev := cmd.PreRunE
	cmd.PreRunE = func(c *cobra.Command, args []string) error {
		if prev != nil {
			if err := prev(c, args); err != nil {
				return err
			}
		}
		return singleFlag(c, name, short, *vals, qualifier)
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
