// Package claudecode is the adapter over Claude Code's PRIVATE session
// registry — the one place in furrow that knows its layout. It implements the
// core.SessionRegistry port for the co-located-session write guard
// (internal/app's session guard) and identifies THIS process's session from
// the environment Claude Code hands its subprocesses.
//
// The format is undocumented and was measured on 2026-09-10 (Claude Code
// 2.1.267, darwin):
//
//   - env: CLAUDECODE=1, CLAUDE_PID=<pid>, CLAUDE_CODE_SESSION_ID=<uuid>;
//   - <config dir>/sessions/<pid>.json: {"pid","sessionId","cwd","startedAt"
//     (epoch ms),"kind","name",…} — no activity field;
//   - <config dir>/projects/<cwd with every non-alphanumeric byte → '-'>/
//     <sessionId>.jsonl is the transcript, whose mtime is the last activity;
//   - <config dir> is CLAUDE_CONFIG_DIR, else ~/.claude.
//
// Everything here is best-effort by contract: a missing registry is "no
// sessions", a dead pid is dropped, an entry that fails to parse is skipped
// (Scan names it for doctor), and only a registry whose EVERY entry is
// unreadable — the shape a format change takes — is an error, on which the
// guard stands down. Nothing in this package blocks a write by itself.
package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

const (
	// EnvActive is set (to "1") in every subprocess Claude Code runs; its
	// presence is what makes furrow an AI write rather than a human's.
	EnvActive = "CLAUDECODE"
	// EnvPID is the Claude Code process id — the registry file's name.
	EnvPID = "CLAUDE_PID"
	// EnvSessionID is the session uuid — the transcript file's name.
	EnvSessionID = "CLAUDE_CODE_SESSION_ID"
	// EnvConfigDir overrides the ~/.claude config directory.
	EnvConfigDir = "CLAUDE_CONFIG_DIR"
)

// Self identifies the Claude Code session THIS process runs under, from the
// environment. ok is false outside Claude Code (no EnvActive) — a human
// shell, CI — which is the guard's off switch. Under Claude Code the PID may
// still be 0 (unset or unparsable); the app then matches the registry by ID.
func Self() (pid int, id string, ok bool) {
	if os.Getenv(EnvActive) == "" {
		return 0, "", false
	}
	pid, _ = strconv.Atoi(strings.TrimSpace(os.Getenv(EnvPID)))
	if pid < 0 {
		pid = 0
	}
	return pid, strings.TrimSpace(os.Getenv(EnvSessionID)), true
}

// DefaultDir is the Claude Code config directory: EnvConfigDir when set, else
// ~/.claude.
func DefaultDir() (string, error) {
	if d := os.Getenv(EnvConfigDir); d != "" {
		return filepath.Abs(d)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// Registry reads the session registry under Dir (a Claude Code config dir).
type Registry struct {
	Dir string
	// alive overrides the process-liveness probe (tests); nil = the real one.
	alive func(pid int) bool
}

var _ core.SessionRegistry = Registry{}

// Exists reports whether Dir holds a sessions/ registry at all — the gate
// doctor uses so a machine without Claude Code raises no finding.
func (r Registry) Exists() bool {
	fi, err := os.Stat(filepath.Join(r.Dir, "sessions"))
	return err == nil && fi.IsDir()
}

// Sessions implements core.SessionRegistry: Scan minus the diagnostics.
func (r Registry) Sessions() ([]core.Session, error) {
	s, _, err := r.Scan()
	return s, err
}

// Scan reads every sessions/*.json entry: live sessions come back sorted by
// PID, and the entries that could not be read or parsed (or lack the fields
// the guard needs) are named in unreadable. err is reserved for a registry
// that exists but yields NOTHING usable — an unlistable directory, or every
// entry unreadable, the shape a format change takes — so the guard can stand
// down and say so instead of silently guarding nothing. No sessions/ dir at
// all is (nil, nil, nil).
func (r Registry) Scan() (sessions []core.Session, unreadable []string, err error) {
	dir := filepath.Join(r.Dir, "sessions")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("list %s: %w", dir, err)
	}
	alive := r.alive
	if alive == nil {
		alive = processAlive
	}
	parsed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		s, ok := r.readEntry(path)
		if !ok {
			unreadable = append(unreadable, path)
			continue
		}
		parsed++
		if !alive(s.PID) {
			continue
		}
		sessions = append(sessions, s)
	}
	if parsed == 0 && len(unreadable) > 0 {
		return nil, unreadable, fmt.Errorf("no readable entry in %s (%d unreadable; the registry format may have changed)", dir, len(unreadable))
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].PID < sessions[j].PID })
	return sessions, unreadable, nil
}

// entry mirrors the fields furrow reads from a sessions/<pid>.json; every
// other key is ignored.
type entry struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	StartedAt int64  `json:"startedAt"`
	Name      string `json:"name"`
}

// readEntry parses one registry file. ok is false when the file cannot be
// read or parsed, or when a field the guard cannot do without (pid,
// sessionId, cwd, startedAt) is missing — a renamed field is a format
// change, not a session, and requiring sessionId is what turns "every
// transcript unfindable, so everyone is busy" into a stand-down.
func (r Registry) readEntry(path string) (core.Session, bool) {
	// #nosec G304 -- path is an entry under the config dir's sessions/
	// registry, listed by ReadDir above; not attacker-supplied.
	data, err := os.ReadFile(path)
	if err != nil {
		return core.Session{}, false
	}
	var e entry
	if err := json.Unmarshal(data, &e); err != nil || e.PID <= 0 || e.SessionID == "" || e.CWD == "" || e.StartedAt <= 0 {
		return core.Session{}, false
	}
	s := core.Session{
		PID: e.PID, ID: e.SessionID, Name: e.Name, CWD: e.CWD,
		StartedAt: time.UnixMilli(e.StartedAt).UTC().Truncate(time.Second),
	}
	if fi, err := os.Stat(r.transcriptPath(e.CWD, e.SessionID)); err == nil {
		s.LastActive = fi.ModTime().UTC().Truncate(time.Second)
	}
	return s, true
}

// transcriptPath is projects/<mangled cwd>/<sessionId>.jsonl under Dir. The
// mangling — every character outside [A-Za-z0-9] becomes '-' — is Claude
// Code's, reproduced here (measured on an ASCII path:
// /Volumes/workspace/github.com/o/r → -Volumes-workspace-github-com-o-r).
// It maps RUNES, not bytes: the original is a JavaScript replace over
// characters, so a non-ASCII path segment becomes one dash per character
// (the byte-wise reading would write three). Unmeasured for non-ASCII; the
// guard's self-transcript check (app.sessionSnapshot) is what catches a
// wrong reading — it stands down instead of calling everyone busy.
func (r Registry) transcriptPath(cwd, sessionID string) string {
	return filepath.Join(r.Dir, "projects", mangleCWD(cwd), sessionID+".jsonl")
}

func mangleCWD(cwd string) string {
	return strings.Map(func(c rune) rune {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			return c
		default:
			return '-'
		}
	}, cwd)
}
