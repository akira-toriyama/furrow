package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
)

// GlobalConfigPath returns the resolved path to the user-level furrow config
// (${XDG_CONFIG_HOME:-~/.config}/furrow/config.toml). It does not check that the
// file exists — `furrow config path` prints it so you can find or create it.
func GlobalConfigPath() (string, error) {
	return globalConfigPath()
}

// GlobalConfigWarnings loads the user-level config and returns its clamp
// warnings (dropped/ignored [[board]] entries, or a malformed-file note),
// regardless of cwd or scope. This is the explicit surface for the warnings that
// discovery DROPS on its inert path (all boards clamped away, or cwd out of every
// scope): `furrow lint` and `furrow config path` report them instead of silently
// ignoring a half-written home config. A missing file is quiet.
func GlobalConfigWarnings() []string {
	path, err := globalConfigPath()
	if err != nil {
		return nil
	}
	_, warn, err := config.LoadGlobalBoards(path)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", path, err)}
	}
	return warn
}

// InitGlobalConfig writes the user-level config template to GlobalConfigPath(),
// refusing to overwrite an existing file. It fills the central board's path and
// scopes from context when it can: flagPath/flagScopes win; otherwise the board
// path is the nearest .furrow enclosing startDir and the scope is that board
// repo's parent (…/<org>/<repo>/.furrow -> …/<org>). With nothing to derive and
// no flags it writes the placeholder template (derived=false). Like `furrow init`
// it writes directly — the read-only-config rule's "just write a template"
// exception. Returns the written path and whether a board was derived or given.
func InitGlobalConfig(startDir, flagPath string, flagScopes []string) (string, bool, error) {
	cfgPath, err := globalConfigPath()
	if err != nil {
		return "", false, err
	}
	// Lstat (not Stat) so a broken symlink at cfgPath still counts as "exists":
	// Stat follows the link and reports ENOENT, and os.WriteFile would then create
	// the link's target — silently writing through it instead of refusing.
	if _, err := os.Lstat(cfgPath); err == nil {
		return "", false, core.Validationf("", "%s already exists; edit it directly (see `furrow config path`) or remove it first", cfgPath)
	}

	boardPath := flagPath
	if boardPath == "" {
		if p, ok := nearestFurrow(startDir); ok {
			boardPath = p
		}
	}
	scopes := flagScopes
	if len(scopes) == 0 && filepath.IsAbs(boardPath) {
		// …/<org>/<repo>/.furrow -> repo …/<org>/<repo> -> scope …/<org>. Only an
		// absolute board path derives a meaningful scope (the context path always
		// is); a relative or ~ --path would yield a useless "." or "~", so leave
		// the placeholder scope for the user to fill instead.
		scopes = []string{filepath.Dir(filepath.Dir(boardPath))}
	}

	content := config.RenderGlobalConfig(boardPath, scopes)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return "", false, core.Internalf("", "create config dir: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		return "", false, core.Internalf("", "write %s: %v", cfgPath, err)
	}
	return cfgPath, boardPath != "", nil
}

// nearestFurrow walks up from startDir and returns the path of the first
// enclosing .furrow directory (the same upward walk discover() does), or
// ok=false when none encloses startDir. `furrow config init` uses it to derive
// the central board path from context.
func nearestFurrow(startDir string) (string, bool) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", false
	}
	for {
		cand := filepath.Join(dir, DirName)
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
