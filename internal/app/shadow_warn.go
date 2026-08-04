package app

import "path/filepath"

// ShadowedDraftWarning explains a bare `add` that just DRAFTED because it ran
// from inside a configured board's own tree: there, plain local discovery
// (source=local) wins over the `[[board]]` entry and carries none of its repo
// scope, so the task lands with `repos: []` and no error — the operator finds
// out at `ls --drafts`, sessions later. `furrow doctor` already detects the
// layout (code `scope-shadowed`); this is the same finding raised at the
// moment it actually bites. Returns "" whenever the situation does not apply.
//
// The check is deliberately the cheap inverse of doctor's scanShadows: instead
// of walking the filesystem it compares the RESOLVED store (a.Dir) against the
// configured board paths, and only for a mutation that really produced a
// draft — so the common paths pay one user-config read at most, and only on
// the failure being warned about. Everything is best-effort: a config that
// cannot be read must not fail an `add` that already succeeded.
//
// A `[[board]]` entry that declares no repo (repo = "") is skipped: reaching
// that board through the user-config arm would have attached nothing either,
// so local resolution lost nothing. A board whose own config.toml names a
// default_repo never gets here at all — applyBoardScope attaches the repo and
// the task is not a draft.
func (a *App) ShadowedDraftWarning(cwd string) string {
	if a.Source != "local" {
		return ""
	}
	boards, cfgDir, _, err := loadGlobalBoards()
	if err != nil || len(boards) == 0 {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	cdir := canonicalPath(abs)
	canonStore := canonicalPath(a.Dir)
	for i := range boards {
		b := &boards[i]
		if b.Repo == "" {
			continue
		}
		board, err := resolvePathRelTo(cfgDir, b.Path)
		if err != nil {
			continue
		}
		if canonicalPath(board) != canonStore {
			// A DIFFERENT nearer store shadowing the board is nearest-wins
			// working as documented (a deliberate opt-out, or a leftover doctor
			// reports); only the board's own tree loses its own scope silently.
			continue
		}
		for _, s := range boardScopes(b, board) {
			if _, ok, scopeErr := canonicalScopeUnder(cdir, cfgDir, s); scopeErr == nil && ok {
				return "warning: drafted — this directory is inside the board's own tree, so discovery ran source=local WITHOUT the [[board]] repo scope; run from the scope root, pass -r <repo>, or give the board a default_repo (`furrow doctor`: scope-shadowed)"
			}
		}
	}
	return ""
}
