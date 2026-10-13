package config

import (
	"fmt"
	"strings"
)

// SessionGuard is the [session].guard mode of the USER-LEVEL config: what the
// co-located-session write guard (internal/app's session guard) does with a
// write that touches a repo an earlier-started AI agent session on this
// machine sits in. It is per-machine on purpose — the sessions it judges are
// this machine's, and a board's committed config.toml would propagate one
// operator's choice to every clone and CI — and it is an enum, not a bool, so
// a later mode (a deferred write attached once the occupant is done) is one
// more value, not a second key. The values name no agent: the adapter that
// reads the registry is internal/claudecode today, and another adapter is a
// detection concern, never a config one.
type SessionGuard string

const (
	// SessionGuardRefuse refuses the write while the occupant is working
	// (exit 2, session-busy) and warns once it is idle. The default.
	SessionGuardRefuse SessionGuard = "refuse"
	// SessionGuardWarn never refuses: every clash, working or idle, is the
	// warning (stderr + session_warn in the envelope).
	SessionGuardWarn SessionGuard = "warn"
	// SessionGuardOff reads no registry: no refusal, no warning, no stand-down
	// note.
	SessionGuardOff SessionGuard = "off"
)

// SessionGuardValues is the closed vocabulary, in the order the docs list it.
func SessionGuardValues() []string {
	return []string{string(SessionGuardRefuse), string(SessionGuardWarn), string(SessionGuardOff)}
}

// GlobalSession is the clamped [session] section of the user-level config.
type GlobalSession struct {
	Guard SessionGuard
	// BusySeconds is how long a mid-turn occupant keeps counting as WORKING
	// after its last transcript write (see DefaultSessionBusySeconds). Always
	// positive after the clamp: warn-only is Guard, not a zero here.
	BusySeconds int
}

// DefaultGlobalSession is the [session] section of a user config that has
// none: the guard refuses, with the stock window.
func DefaultGlobalSession() GlobalSession {
	return GlobalSession{Guard: SessionGuardRefuse, BusySeconds: DefaultSessionBusySeconds}
}

type rawSession struct {
	Guard       string `toml:"guard"`
	BusySeconds *int   `toml:"busy_seconds"`
}

// clampSession applies clamp-don't-reject to the [session] section: a guard
// outside the vocabulary and a non-positive window each fall back to the
// default with a warning naming the file and the accepted values — the
// warning `furrow config set --user` refuses to introduce.
func clampSession(r rawSession, path string) (GlobalSession, []string) {
	s := DefaultGlobalSession()
	var warn []string
	if r.Guard != "" {
		switch g := SessionGuard(r.Guard); g {
		case SessionGuardRefuse, SessionGuardWarn, SessionGuardOff:
			s.Guard = g
		default:
			warn = append(warn, fmt.Sprintf("%s: session.guard %q is not one of %s; using %q", path, r.Guard, strings.Join(SessionGuardValues(), "|"), SessionGuardRefuse))
		}
	}
	if r.BusySeconds != nil {
		if *r.BusySeconds < 1 {
			warn = append(warn, fmt.Sprintf("%s: session.busy_seconds %d is not a positive count; using %d (warn-only is session.guard = %q, not a zero window)", path, *r.BusySeconds, DefaultSessionBusySeconds, SessionGuardWarn))
		} else {
			s.BusySeconds = *r.BusySeconds
		}
	}
	return s, warn
}

// coerceSession is clampSession's untyped twin for the salvage path (a type
// error somewhere in the file): a key holding the wrong type is dropped with
// a warning, the rest of the section still reads.
func coerceSession(v any, path string) (GlobalSession, []string) {
	m, ok := v.(map[string]any)
	if v == nil {
		return DefaultGlobalSession(), nil
	}
	if !ok {
		return DefaultGlobalSession(), []string{fmt.Sprintf("%s: `session` is not a [session] table; ignoring it", path)}
	}
	var r rawSession
	var warn []string
	for key, val := range m {
		switch key {
		case "guard":
			s, ok := val.(string)
			if !ok {
				warn = append(warn, fmt.Sprintf("%s: [session]: key %q has the wrong type; ignoring it", path, key))
				continue
			}
			r.Guard = s
		case "busy_seconds":
			n, ok := val.(int64)
			if !ok {
				warn = append(warn, fmt.Sprintf("%s: [session]: key %q has the wrong type; ignoring it", path, key))
				continue
			}
			i := int(n)
			r.BusySeconds = &i
		default:
			warn = append(warn, fmt.Sprintf("%s: [session]: unknown key %q; ignored", path, key))
		}
	}
	s, w := clampSession(r, path)
	return s, append(warn, w...)
}
