package app

import (
	"fmt"
	"sort"

	"github.com/akira-toriyama/furrow/internal/core"
)

// Lint runs the full consistency check: core's in-memory rules plus the
// filesystem-level index<->body 1:1 mapping (every task has a body file and
// vice versa). Config clamp warnings are surfaced too, so `furrow lint` is the
// one place that tells you everything that is off.
func (a *App) Lint() ([]core.Problem, error) {
	idx, err := a.Store.Load()
	if err != nil {
		return nil, err
	}
	// The estimate range check runs on the RAW (pre-clamp) index: Canonicalize
	// would otherwise round a hand-edited out-of-range value/effort away before
	// we could warn about it. Everything else is order-independent, so we
	// canonicalize after and validate as before.
	ps := core.EstimateProblems(idx)
	core.Canonicalize(idx, a.Cfg.Lanes)
	ps = append(ps, core.Validate(idx, a.Cfg.Lanes, a.Cfg.IDPattern())...)

	// index <-> body 1:1.
	bodyIDs, err := a.Store.ListBodyIDs()
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, id := range bodyIDs {
		have[id] = true
	}
	for _, t := range idx.Tasks {
		if !have[t.ID] {
			ps = append(ps, core.Problem{Severity: core.SevError, ID: t.ID, Msg: fmt.Sprintf("task has no body file (%s)", core.BodyPath(t.ID))})
		}
	}
	inIndex := map[string]bool{}
	for _, t := range idx.Tasks {
		inIndex[t.ID] = true
	}
	for _, id := range bodyIDs {
		if !inIndex[id] {
			ps = append(ps, core.Problem{Severity: core.SevWarn, ID: id, Msg: fmt.Sprintf("orphan body file %s has no task in the index", core.BodyPath(id))})
		}
	}

	// required-label rule (config [labels].required).
	if a.Cfg.LabelsRequired {
		for _, t := range idx.Tasks {
			if len(t.Labels) == 0 {
				ps = append(ps, core.Problem{Severity: core.SevError, ID: t.ID, Msg: "task has no label ([labels].required)"})
			}
		}
	}

	// surface config clamp warnings as lint warns.
	for _, w := range a.Warnings {
		ps = append(ps, core.Problem{Severity: core.SevWarn, ID: "config", Msg: w})
	}

	// surface user-level (home) config clamp warnings too. Discovery drops these
	// on its inert path — a half-written ~/.config/furrow/config.toml whose boards
	// all clamp away leaves no board AND no signal — so lint is where they land
	// (running it once is explicit, unlike spamming every command's stderr).
	for _, w := range GlobalConfigWarnings() {
		ps = append(ps, core.Problem{Severity: core.SevWarn, ID: "global-config", Msg: w})
	}

	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Severity != ps[j].Severity {
			return ps[i].Severity < ps[j].Severity
		}
		if ps[i].ID != ps[j].ID {
			return ps[i].ID < ps[j].ID
		}
		return ps[i].Msg < ps[j].Msg
	})
	return ps, nil
}
