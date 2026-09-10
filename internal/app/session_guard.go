package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// The co-located-session write guard.
//
// Why: on 2026-09-10 a Claude Code session chatting with the human ran `furrow
// add` into a repo where ANOTHER session was working autonomously, having
// judged "creating a task is not interference". A rule that asks the agent to
// judge interference is broken by judgment; this is the mechanization. It is
// best-effort by contract (the registry is another tool's private format):
// wherever the facts cannot be read, the guard stands down and SAYS so on
// stderr — it never refuses on a guess, and it never blocks a human.
//
// The rule, in one sentence: a write from a Claude Code session that touches a
// repo an EARLIER-started session on this machine sits in is refused
// (session-busy) while that session is working, and warned about
// (session_warn) while it is idle. Repos come from the entity being written —
// a new task's repos after the board-scope union (so a bare `add` inside a
// checkout is guarded), and an existing task's or box's repos before AND after
// the edit (so `--add-repo`/`--rm-repo` are judged on both sides). First come,
// first served: the earlier session's own writes never clash, so an
// autonomous loop is never stopped by the session that came to watch it.
//
// Not guarded, deliberately: reads; board maintenance (`archive`, `tidy`,
// `upgrade`, `review`'s clock stamps, `sync`); and anything outside Claude
// Code (no CLAUDECODE env — a human shell, CI).

// SessionRef identifies THIS process's session for the guard: the registry
// entry with the same PID, or failing that the same non-empty ID.
type SessionRef struct {
	PID int
	ID  string
}

// SessionWarnLine renders the idle-occupant warning the CLI prints on stderr
// when a guarded write went through beside an idle earlier session.
func SessionWarnLine(clashes []core.SessionClash) string {
	parts := make([]string, 0, len(clashes))
	for _, c := range clashes {
		parts = append(parts, fmt.Sprintf("%s is also open in the earlier Claude Code session %s (idle %s)", c.Repo, sessionLabel(c), idleLabel(c)))
	}
	return strings.Join(parts, "; ") + " — the write went through; if that session resumes, it may collide with this edit"
}

// TakeSessionWarn hands the CLI the idle clashes the guard recorded during
// this process's writes, and clears them — so the warning is emitted once
// whichever path (an envelope annotate, the post-run hook) drains it first.
func (a *App) TakeSessionWarn() []core.SessionClash {
	w := a.sessionWarn
	a.sessionWarn = nil
	return w
}

// TakeSessionNotes drains the guard's stand-down notes (a registry it could
// not read, a self it could not find) for stderr.
func (a *App) TakeSessionNotes() []string {
	n := a.sessionNotes
	a.sessionNotes = nil
	return n
}

// guardTask guards a write to an existing task, judged on the repos it
// carries now (call it before the edit) — the shorthand every batch path uses.
func (a *App) guardTask(t *core.Task) error {
	return a.guardRepos(t.ID, t.Repos, "")
}

// guardRepos is the guard itself. subject tags the error (a task/epic id, ""
// for a creation); repos is the union the write touches; hint, when non-empty,
// is the escape the message and `details.hint` name (`--draft` for a task
// add). Busy clashes refuse the write; idle ones are recorded for the CLI to
// warn about; no registry, no self, or a repo the guard cannot derive is no
// clash.
func (a *App) guardRepos(subject string, repos []string, hint string) error {
	if a.Sessions == nil || len(repos) == 0 {
		return nil
	}
	self, others, ok := a.sessionSnapshot()
	if !ok {
		return nil
	}
	within := time.Duration(a.Cfg.SessionBusySeconds) * time.Second
	clashes := core.SessionClashes(self, others, a.sessionRepoOf, repos, a.Clock.Now(), within)
	if len(clashes) == 0 {
		return nil
	}
	busy := false
	for _, c := range clashes {
		busy = busy || c.Busy
	}
	if !busy {
		a.sessionWarn = mergeClashes(a.sessionWarn, clashes)
		return nil
	}
	return sessionBusyErr(subject, clashes, hint)
}

// sessionSnapshot reads the registry once per process and locates self. ok is
// false — with one stderr note queued, once — when the registry cannot be
// read or this session is not in it: without an order between sessions the
// guard has nothing truthful to refuse on, so it stands down rather than
// inventing one (refusing an unregistered autonomous session's own writes
// would be a NEW failure, worse than the one being prevented).
func (a *App) sessionSnapshot() (self core.Session, others []core.Session, ok bool) {
	if !a.sessionRead {
		a.sessionRead = true
		sessions, err := a.Sessions.Sessions()
		if err != nil {
			a.sessionNotes = append(a.sessionNotes, fmt.Sprintf("session guard: cannot read the Claude Code session registry (%v); standing down for this write", err))
			return core.Session{}, nil, false
		}
		a.sessionOthers = sessions
		found := false
		for _, s := range sessions {
			if (a.Self.PID > 0 && s.PID == a.Self.PID) || (a.Self.ID != "" && s.ID == a.Self.ID) {
				a.sessionSelf, found = s, true
				break
			}
		}
		if !found {
			a.sessionNotes = append(a.sessionNotes, fmt.Sprintf("session guard: this session (pid %d) is not in the Claude Code session registry, so its writes cannot be ordered against other sessions; standing down", a.Self.PID))
		}
		a.sessionSelfOK = found
	}
	return a.sessionSelf, a.sessionOthers, a.sessionSelfOK
}

// sessionRepoOf is the repoOf the pure decision takes: a session's cwd to the
// owner/repo of its enclosing checkout, by the same file-only derivation the
// `repo = "auto"` scope uses (worktree-aware), memoized per cwd.
func (a *App) sessionRepoOf(cwd string) string {
	if r, ok := a.sessionRepos[cwd]; ok {
		return r
	}
	if a.sessionRepos == nil {
		a.sessionRepos = map[string]string{}
	}
	r, _ := repoForDir(cwd)
	a.sessionRepos[cwd] = r
	return r
}

// sessionBusyErr is the refusal: exit 2, kind session-busy, every clash in
// details (busy and idle alike, flagged), and the escape in the message.
func sessionBusyErr(subject string, clashes []core.SessionClash, hint string) *core.Error {
	var who []string
	for _, c := range clashes {
		if !c.Busy {
			continue
		}
		who = append(who, fmt.Sprintf("%s is being worked on by the earlier Claude Code session %s (%s)", c.Repo, sessionLabel(c), activityLabel(c)))
	}
	msg := strings.Join(who, "; ")
	details := map[string]any{"clashes": clashes}
	if hint != "" {
		msg += " — create it as a draft instead (" + hint + ", then `furrow repo <id> --add <repo>` once that session is done), or leave the write to that session"
		details["hint"] = hint
	} else {
		msg += " — wait for that session to go idle, hand the write to it, or run it from a shell outside Claude Code"
	}
	return &core.Error{Code: core.CodeValidation, Kind: core.KindSessionBusy, Subject: subject, Msg: msg, Details: details}
}

func sessionLabel(c core.SessionClash) string {
	if c.Name != "" {
		return fmt.Sprintf("%s, pid %d", c.Name, c.PID)
	}
	return fmt.Sprintf("pid %d", c.PID)
}

func activityLabel(c core.SessionClash) string {
	if c.IdleSeconds == nil {
		return "activity unknown, treated as working"
	}
	return "active " + humanDuration(*c.IdleSeconds) + " ago"
}

func idleLabel(c core.SessionClash) string {
	if c.IdleSeconds == nil {
		return "unknown"
	}
	return humanDuration(*c.IdleSeconds)
}

func humanDuration(secs int) string {
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm", secs/60)
	default:
		return fmt.Sprintf("%dh%dm", secs/3600, (secs%3600)/60)
	}
}

// mergeClashes unions a write's idle clashes into the per-process record,
// keyed by repo+pid, sorted like the decision's output.
func mergeClashes(have, add []core.SessionClash) []core.SessionClash {
	seen := map[string]bool{}
	out := make([]core.SessionClash, 0, len(have)+len(add))
	for _, c := range append(append([]core.SessionClash(nil), have...), add...) {
		k := fmt.Sprintf("%s\x00%d", c.Repo, c.PID)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].PID < out[j].PID
	})
	return out
}

// unionRepos is the before∪after repo set an edit is judged on.
func unionRepos(before, after []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range append(append([]string(nil), before...), after...) {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}
