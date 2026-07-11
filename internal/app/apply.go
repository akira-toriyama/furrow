package app

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// ApplyMode is when directives are applied: at PR open or PR merge. It changes
// what status (if any) a directive sets — open never reaches a terminal lane,
// so a directive targeting `done` only closes the task at merge.
type ApplyMode string

const (
	OnOpen  ApplyMode = "open"
	OnMerge ApplyMode = "merge"
)

// DefaultOpenLane is the lane a task is nudged to when a PR referencing it is
// opened. It is overridable (--open-lane) for non-default lane schemes.
const DefaultOpenLane = "in-progress"

// Directive is one `SetStatus-task:` line parsed from PR/commit text. Link is
// the first token verbatim (a body-file URL or a bare id); ID is the task id
// pulled out of it; Lane is the optional merge-target lane (empty = annotate
// only, the GTD "just referenced" case).
type Directive struct {
	Link string
	ID   string
	Lane string
	Raw  string
}

var (
	// directiveRe matches a `SetStatus-task:` line (case-insensitive key,
	// tolerant of leading markdown list/quote markers) and captures the rest.
	directiveRe = regexp.MustCompile(`(?im)^[ \t>*-]*SetStatus-task:[ \t]*(.+?)[ \t]*$`)
	// idFromLink pulls the body-file stem out of a .../bodies/<id>.md link. It is
	// prefix-agnostic (does not assume "t-") since the store owns the id format.
	idFromLink = regexp.MustCompile(`/bodies/([A-Za-z0-9_-]+)\.md`)
)

// ParseDirectives extracts every SetStatus-task directive from arbitrary text
// (a PR body, a commit message). The id is taken from a /bodies/<id>.md link,
// else the first token verbatim (a bare id). Lines with no payload are skipped.
func ParseDirectives(text string) []Directive {
	var out []Directive
	for _, m := range directiveRe.FindAllStringSubmatch(text, -1) {
		fields := strings.Fields(m[1])
		if len(fields) == 0 {
			continue
		}
		d := Directive{Link: fields[0], Raw: strings.TrimSpace(m[0])}
		if len(fields) >= 2 {
			d.Lane = fields[1]
		}
		if sm := idFromLink.FindStringSubmatch(fields[0]); sm != nil {
			d.ID = sm[1]
		} else {
			d.ID = fields[0]
		}
		out = append(out, d)
	}
	return out
}

// ApplyOutcome is what happened for one directive (one row of JSON output).
type ApplyOutcome struct {
	ID     string `json:"id"`
	Lane   string `json:"lane,omitempty"`  // the directive's merge-target lane
	Action string `json:"action"`          // moved | annotated | skipped | error
	To     string `json:"to,omitempty"`    // resulting status if moved
	Note   string `json:"note,omitempty"`  // body line appended (if any)
	Error  string `json:"error,omitempty"` // message when Action == error
	Code   int    `json:"code,omitempty"`  // furrow exit code for this directive's error
	// Candidates carries the concrete alternatives when a directive's error
	// almost resolved (an unknown lane → the configured lanes), so an agent
	// triaging a batch branches on the array rather than regexing the message.
	Candidates []string `json:"candidates,omitempty"`
}

// ApplyResult is the full report — the JSON output of `furrow apply`.
type ApplyResult struct {
	On       string         `json:"on"`
	Ref      string         `json:"ref,omitempty"`
	Outcomes []ApplyOutcome `json:"outcomes"`
}

// WorstCode returns the most severe per-directive exit code, so the CLI can exit
// non-zero when any directive failed while still having applied the valid ones.
func (r ApplyResult) WorstCode() int {
	worst := int(core.CodeOK)
	for _, o := range r.Outcomes {
		if o.Code > worst {
			worst = o.Code
		}
	}
	return worst
}

// ApplyDirectives parses SetStatus-task directives from text and applies them to
// the store per mode:
//
//   - OnMerge: move the task to the directive's lane (empty lane = annotate only).
//   - OnOpen:  nudge a non-terminal task to openLane (default in-progress), but
//     only when the directive carries a lane. The directive's own lane is NOT
//     applied on open — that is the merge target — so a terminal lane like `done`
//     is structurally unreachable at open.
//
// When ref is non-empty each touched task gets a one-line body annotation
// recording the ref + event, deduped by exact line so re-runs are idempotent.
// Validation (unknown id/lane) is per-directive and non-fatal: invalid directives
// are reported with a non-zero Code while valid ones still apply. A returned
// error is reserved for IO failures (the store layer).
func (a *App) ApplyDirectives(text, ref string, mode ApplyMode, openLane string) (ApplyResult, error) {
	if openLane == "" {
		openLane = DefaultOpenLane
	}
	res := ApplyResult{On: string(mode), Ref: ref, Outcomes: []ApplyOutcome{}}

	for _, d := range ParseDirectives(text) {
		out := ApplyOutcome{ID: d.ID, Lane: d.Lane, Action: "skipped"}

		switch {
		case d.ID == "":
			fail(&out, core.Validationf("", "could not parse a task id from %q", d.Link))
		case !a.exists(d.ID):
			fail(&out, core.NotFound(d.ID))
		case d.Lane != "" && !a.Cfg.IsLane(d.Lane):
			fail(&out, a.unknownLaneErr(d.ID, d.Lane))
		default:
			if err := a.applyOne(&out, d, ref, mode, openLane); err != nil {
				return res, err // IO failure: abort
			}
		}
		res.Outcomes = append(res.Outcomes, out)
	}
	return res, nil
}

// applyOne performs the status move (if any) and body annotation for a single
// validated directive, recording the result in out. It returns an error only on
// an IO failure (which aborts the whole run); per-directive validation problems
// are recorded in out, not returned.
func (a *App) applyOne(out *ApplyOutcome, d Directive, ref string, mode ApplyMode, openLane string) error {
	// Fetch once: needed for the terminal check (open) and the no-op skip below.
	t, _, err := a.Get(d.ID)
	if err != nil {
		return err
	}

	// Pick the lane to set for THIS event.
	target := ""
	switch mode {
	case OnMerge:
		target = d.Lane // empty => annotate only
	case OnOpen:
		if d.Lane != "" && !a.Cfg.IsTerminal(t.Status) {
			if !a.Cfg.IsLane(openLane) {
				fail(out, &core.Error{
					Code:       core.CodeValidation,
					ID:         d.ID,
					Msg:        fmt.Sprintf("--open-lane %q is not a configured lane (configured: %s)", openLane, strings.Join(a.Cfg.Lanes, ", ")),
					Candidates: append([]string(nil), a.Cfg.Lanes...),
				})
				return nil
			}
			target = openLane
		}
	}

	switch {
	case target == "" || t.Status == target:
		// No lane change needed. When already in the target lane, skip the move
		// so a re-run (or a no-op event) doesn't churn the `updated` stamp or the
		// tracker's git history — keeping `apply` truly idempotent.
		if target != "" {
			out.To = t.Status
		}
	default:
		moved, err := a.Move(d.ID, target)
		if err != nil {
			if core.ExitCode(err) >= int(core.CodeInternal) {
				return err
			}
			fail(out, err)
			return nil
		}
		out.Action, out.To = "moved", moved.Status
	}

	if ref != "" {
		line := annotationLine(mode, ref, d.Lane)
		changed, err := a.AppendBody(d.ID, line)
		if err != nil {
			return err
		}
		if changed {
			out.Note = line
			if out.Action == "skipped" {
				out.Action = "annotated"
			}
		}
	}
	return nil
}

// fail records a per-directive error onto out, carrying any machine-actionable
// candidates (e.g. the configured lanes for an unknown-lane directive) through
// to the outcome so a batch consumer branches on the array, not the prose.
func fail(out *ApplyOutcome, err error) {
	out.Action = "error"
	out.Error = err.Error()
	out.Code = core.ExitCode(err)
	if fe := core.AsError(err); fe != nil && len(fe.Candidates) > 0 {
		out.Candidates = fe.Candidates
	}
}

// exists reports whether a task id is present, cheaply (no body load).
func (a *App) exists(id string) bool {
	idx, err := a.load()
	if err != nil {
		return false
	}
	return idx.Has(id)
}

// annotationLine renders the one-line body note recording a PR event. It mirrors
// the existing hand-written convention (a ✅ bullet citing the merged PR).
func annotationLine(mode ApplyMode, ref, lane string) string {
	switch mode {
	case OnOpen:
		return "- 🚧 `" + ref + "` opened"
	default: // merge
		if lane != "" {
			return "- ✅ `" + ref + "` merged → `" + lane + "`"
		}
		return "- 🔗 `" + ref + "` merged"
	}
}

// AppendBody appends line (plus a newline) to a task's body, unless an identical
// line is already present — so re-running `apply` for the same PR event is
// idempotent. Returns whether the body changed. The id must exist.
func (a *App) AppendBody(id, line string) (bool, error) {
	idx, err := a.load()
	if err != nil {
		return false, err
	}
	if !idx.Has(id) {
		return false, core.NotFound(id)
	}
	body, err := a.Store.LoadBody(id)
	if err != nil {
		return false, err
	}
	if strings.Contains(body, line) {
		return false, nil
	}
	var b strings.Builder
	b.WriteString(body)
	if body != "" && !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(line)
	b.WriteString("\n")
	if err := a.Store.SaveBody(id, b.String()); err != nil {
		return false, err
	}
	return true, nil
}
