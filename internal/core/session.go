package core

import (
	"sort"
	"time"
)

// Session is one live session of an AI coding harness on this machine, as the
// SessionRegistry adapter reports it — only the facts the write guard needs.
// A zero LastActive means the adapter found no activity record for it; the
// guard reads unknown activity as busy (the side that protects the occupant).
type Session struct {
	PID        int
	ID         string
	Name       string
	CWD        string
	StartedAt  time.Time
	LastActive time.Time
}

// SessionClash is one (repo, session) pair the guard found: a repo the write
// touches that an EARLIER-started session occupies. Busy is the block/warn
// switch. Times are UTC whole seconds; LastActive/IdleSeconds are null when the
// occupant's activity is unknown (which is itself busy).
type SessionClash struct {
	Repo        string     `json:"repo"`
	PID         int        `json:"pid"`
	SessionID   string     `json:"session_id"`
	Name        string     `json:"name,omitempty"`
	CWD         string     `json:"cwd"`
	StartedAt   time.Time  `json:"started_at"`
	LastActive  *time.Time `json:"last_active"`
	IdleSeconds *int       `json:"idle_seconds"`
	Busy        bool       `json:"busy"`
}

// SessionClashes is the write guard's decision, pure. A write from self that
// touches repos clashes with every other session that (a) is not self (same
// PID, or same non-empty ID — a resumed session), (b) started strictly BEFORE
// self (first come, first served: the earlier session owns the repo, so the
// earlier session's own writes never clash), and (c) sits in a checkout whose
// repo — repoOf(cwd), "" when underivable — is one the write touches. A clash
// is Busy when the occupant's LastActive is unknown or within busyWithin of
// now; busyWithin <= 0 makes every KNOWN activity idle (warn only).
//
// A self with a zero StartedAt cannot be ordered against anyone and yields no
// clash: the caller decides what "self not in the registry" means (the app
// stands down and says so) — the decision here never guesses an order.
// Output is sorted by repo, then occupant start, then PID.
func SessionClashes(self Session, others []Session, repoOf func(cwd string) string, repos []string, now time.Time, busyWithin time.Duration) []SessionClash {
	if self.StartedAt.IsZero() || len(repos) == 0 {
		return nil
	}
	touched := make(map[string]bool, len(repos))
	for _, r := range repos {
		touched[r] = true
	}
	var out []SessionClash
	for _, o := range others {
		if o.PID == self.PID || (o.ID != "" && o.ID == self.ID) {
			continue
		}
		if !o.StartedAt.Before(self.StartedAt) {
			continue
		}
		repo := repoOf(o.CWD)
		if repo == "" || !touched[repo] {
			continue
		}
		c := SessionClash{
			Repo: repo, PID: o.PID, SessionID: o.ID, Name: o.Name, CWD: o.CWD,
			StartedAt: o.StartedAt.UTC().Truncate(time.Second), Busy: true,
		}
		if !o.LastActive.IsZero() {
			last := o.LastActive.UTC().Truncate(time.Second)
			idle := int(now.Sub(last) / time.Second)
			if idle < 0 {
				idle = 0
			}
			c.LastActive, c.IdleSeconds = &last, &idle
			c.Busy = busyWithin > 0 && now.Sub(last) <= busyWithin
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.Before(out[j].StartedAt)
		}
		return out[i].PID < out[j].PID
	})
	return out
}
