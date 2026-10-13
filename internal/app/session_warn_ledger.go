// The session guard's warning ledger: which idle clashes THIS Claude Code
// session has already been told about, kept across furrow processes so the
// stderr line is news, not noise.

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// EnvStateHome is the XDG base for furrow's per-user, per-machine state —
// nothing a board or a config carries: the warning ledger lives in
// $XDG_STATE_HOME/furrow/ (default ~/.local/state/furrow/).
const EnvStateHome = "XDG_STATE_HOME"

// warnLedgerFile is the ledger's name under the state dir.
const warnLedgerFile = "session-warn.json"

// warnEntry records one (self session, occupant session, repo) warning: the
// occupant's activity stamp when it was last printed (nil = unknown then),
// and when. The occupant is keyed by session id, never pid (a pid is reused).
type warnEntry struct {
	Self       string     `json:"self"`
	Occupant   string     `json:"occupant"`
	Repo       string     `json:"repo"`
	LastActive *time.Time `json:"last_active"`
	At         time.Time  `json:"at"`
}

type warnLedger struct {
	Warned []warnEntry `json:"warned"`
}

// UnheardClashes returns the idle clashes this session has NOT been told
// about — never printed for this (occupant, repo), or printed, but the
// occupant has been active since — and records every clash in clashes as
// told. The envelope's `session_warn` is the caller's and carries all of
// them; this decides the stderr line alone.
//
// Why: the line is useful once (a collision is possible) and noise on every
// write after that. On 2026-10-07 a session that ran ~40 mutations got the
// same line 40 times, and the reader learned to drop `^warning` — which is
// how the one line that would have mattered (the occupant came back) gets
// dropped too. "News" is: a pair not yet reported, or an occupant that has
// done something since the report — its transcript's last activity moved —
// so a quiet occupant is reported once per session, and one that resumes and
// goes idle again is reported again. The record is per self session, so a
// new Claude Code session starts with a clean slate (it has not heard
// anything), and it is kept on disk because every furrow command is its own
// process.
//
// Best-effort like the guard: a self without a session id, or a state dir
// that cannot be resolved, means every clash is news (the pre-ledger
// behaviour); a ledger that cannot be parsed starts over; a ledger that
// cannot be written queues one stand-down note and the line repeats next
// time. Entries whose self or occupant session is no longer live are pruned
// on every write, so the file holds only what this machine's sessions can
// still collide over.
func (a *App) UnheardClashes(clashes []core.SessionClash) []core.SessionClash {
	if len(clashes) == 0 || a.Self.ID == "" {
		return clashes
	}
	path, err := warnLedgerPath()
	if err != nil {
		return clashes
	}
	ledger := readWarnLedger(path)
	now := a.Clock.Now().UTC().Truncate(time.Second)
	live := a.liveSessionIDs()
	var fresh []core.SessionClash
	for _, c := range clashes {
		i := ledger.find(a.Self.ID, c.SessionID, c.Repo)
		heard := i >= 0 && !activeSince(ledger.Warned[i].LastActive, c.LastActive)
		if !heard {
			fresh = append(fresh, c)
		}
		e := warnEntry{Self: a.Self.ID, Occupant: c.SessionID, Repo: c.Repo, LastActive: c.LastActive, At: now}
		if i >= 0 {
			ledger.Warned[i] = e
		} else {
			ledger.Warned = append(ledger.Warned, e)
		}
	}
	if live != nil {
		ledger.prune(live)
	}
	if err := writeWarnLedger(path, ledger); err != nil {
		a.sessionNotes = append(a.sessionNotes, fmt.Sprintf("session guard: cannot record this warning in %s (%v); it will repeat on the next write", path, err))
	}
	return fresh
}

// activeSince reports whether an occupant whose activity read `then` at the
// last warning has been active since (`now` is its current reading). An
// unknown reading on either side is "not known to have moved".
func activeSince(then, now *time.Time) bool {
	return then != nil && now != nil && now.After(*then)
}

// liveSessionIDs is the registry snapshot's session ids (self included), or
// nil when the registry was not read in this process.
func (a *App) liveSessionIDs() map[string]bool {
	if !a.sessionRead {
		return nil
	}
	ids := make(map[string]bool, len(a.sessionOthers)+1)
	for _, s := range a.sessionOthers {
		ids[s.ID] = true
	}
	ids[a.Self.ID] = true
	return ids
}

func (l *warnLedger) find(self, occupant, repo string) int {
	for i, e := range l.Warned {
		if e.Self == self && e.Occupant == occupant && e.Repo == repo {
			return i
		}
	}
	return -1
}

func (l *warnLedger) prune(live map[string]bool) {
	kept := l.Warned[:0]
	for _, e := range l.Warned {
		if live[e.Self] && live[e.Occupant] {
			kept = append(kept, e)
		}
	}
	l.Warned = kept
	sort.Slice(l.Warned, func(i, j int) bool {
		a, b := l.Warned[i], l.Warned[j]
		if a.Self != b.Self {
			return a.Self < b.Self
		}
		if a.Occupant != b.Occupant {
			return a.Occupant < b.Occupant
		}
		return a.Repo < b.Repo
	})
}

// warnLedgerPath is $XDG_STATE_HOME/furrow/session-warn.json when the
// variable is an absolute path, else ~/.local/state/furrow/session-warn.json
// — the state twin of globalConfigPath's config rule.
func warnLedgerPath() (string, error) {
	if xdg := os.Getenv(EnvStateHome); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "furrow", warnLedgerFile), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "furrow", warnLedgerFile), nil
}

// readWarnLedger loads the ledger; a missing or unparsable file is an empty
// one (the next write replaces it — a ledger is a cache of what was said,
// never a record worth failing over).
func readWarnLedger(path string) *warnLedger {
	l := &warnLedger{}
	data, err := os.ReadFile(path) // #nosec G304 -- furrow's own state file under the user's state dir
	if err != nil {
		return l
	}
	if err := json.Unmarshal(data, l); err != nil {
		return &warnLedger{}
	}
	return l
}

// writeWarnLedger lands the ledger atomically (temp + rename in its dir), so
// two furrow processes racing leave one whole file — the loser's entries
// repeat one warning at worst, never corrupt the file.
func writeWarnLedger(path string, l *warnLedger) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".session-warn-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
