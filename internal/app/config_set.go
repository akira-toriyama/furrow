package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
)

// ConfigEdit is `furrow config set`'s report: which file, which key, what the
// file said before (null when the key was absent) and says now, and whether
// anything actually changed — the config twin of the task mutators'
// {before, after, changed} envelope, at key granularity because a config write
// touches one key by design.
type ConfigEdit struct {
	File    string   `json:"file"`
	Key     string   `json:"key"`
	Before  any      `json:"before"`
	After   any      `json:"after"`
	Changed []string `json:"changed"`
}

// ConfigSetBoard sets one key of the resolved BOARD's .furrow/config.toml —
// the write side of a file every other command only reads. The edit is
// surgical (config.SetInSection: comments, ordering, and every untouched byte
// survive), and the WRITER is strict where the reader is lenient: an unknown
// key is exit 2 with the key vocabulary in candidates (writing it would create
// a key nothing ever deletes), and a value the reader would clamp away with a
// warning is refused BEFORE the write — the edited document is re-parsed with
// the real loader, and any warning the old document did not carry aborts the
// set. What you wrote is exactly what a read will honor, or nothing changed.
func (a *App) ConfigSetBoard(dotted, value string) (*ConfigEdit, error) {
	if a.Dir == "" {
		return nil, core.Internalf("config", "this store is not file-backed; there is no config.toml to edit")
	}
	k, name, ok := config.ResolveKey(config.BoardKeys(), dotted)
	if !ok {
		return nil, unknownConfigKeyErr(dotted, config.BoardKeys())
	}
	typed, err := config.ParseCLIValue(k, value)
	if err != nil {
		return nil, core.Validationf("config", "%s: %v", dotted, err)
	}
	path := filepath.Join(a.Dir, "config.toml")
	// #nosec G304 -- the board's own config path, derived from the resolved
	// store dir, never caller-supplied.
	data, rerr := os.ReadFile(path)
	if rerr != nil && !os.IsNotExist(rerr) {
		return nil, core.Internalf("config", "read %s: %v", path, rerr)
	}
	// The writer refuses a document the strict reader cannot fully parse: an
	// edit on top of a syntax error would be guesswork.
	if _, _, err := config.LoadBytes(data, path); err != nil && len(data) > 0 {
		return nil, core.Validationf("config", "%s does not parse — fix it before writing through it: %v", path, err)
	}
	res, err := config.SetInSection(string(data), k.Section, name, config.RenderTOMLValue(typed))
	if err != nil {
		return nil, core.Validationf("config", "%s: %v", path, err)
	}
	if err := guardConfigRegression(path, data, []byte(res.Doc), boardWarnings); err != nil {
		return nil, err
	}
	return finishConfigEdit(path, dotted, typed, res)
}

// ConfigSetUser sets one key of a [[board]] entry in the user-level config
// (~/.config/furrow/config.toml). boardRef picks the entry — the epic-ref
// contract carried over: exact path/scope, else unique substring over both,
// else exit 2 with the entry paths in candidates; with exactly one entry the
// ref may be omitted. Entry CREATION is deliberately out of scope (`config
// init` and the operator's editor own the file's shape; the writer edits what
// exists). Package-level, not a method: the user config exists independent of
// any resolved board, exactly like GlobalConfigPath/InitGlobalConfig.
func ConfigSetUser(boardRef, dotted, value string) (*ConfigEdit, error) {
	path, err := GlobalConfigPath()
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- furrow's own user-level config location.
	data, rerr := os.ReadFile(path)
	if os.IsNotExist(rerr) {
		return nil, core.Validationf("config", "no user config at %s — write one with `furrow config init`", path)
	}
	if rerr != nil {
		return nil, core.Internalf("config", "read %s: %v", path, rerr)
	}
	entries, err := config.UserBoardEntries(data, path)
	if err != nil {
		return nil, core.Validationf("config", "%s does not parse strictly — fix it before writing through it: %v", path, err)
	}
	idx, err := resolveUserBoard(entries, boardRef, path)
	if err != nil {
		return nil, err
	}
	k, name, ok := config.ResolveKey(config.UserEntryKeys(), dotted)
	if !ok {
		return nil, unknownConfigKeyErr(dotted, config.UserEntryKeys())
	}
	typed, err := config.ParseCLIValue(k, value)
	if err != nil {
		return nil, core.Validationf("config", "%s: %v", dotted, err)
	}
	res, err := config.SetInArrayTable(string(data), "board", idx, name, config.RenderTOMLValue(typed))
	if err != nil {
		return nil, core.Validationf("config", "%s: %v", path, err)
	}
	if err := guardConfigRegression(path, data, []byte(res.Doc), userWarnings); err != nil {
		return nil, err
	}
	return finishConfigEdit(path, dotted, typed, res)
}

func boardWarnings(data []byte, path string) ([]string, error) {
	_, warn, err := config.LoadBytes(data, path)
	return warn, err
}

func userWarnings(data []byte, path string) ([]string, error) {
	_, warn, err := config.LoadGlobalBoardsBytes(data, path)
	return warn, err
}

// guardConfigRegression re-parses the edited document with the REAL loader and
// refuses the write when it fails to parse or gained a clamp warning the old
// document did not carry — the moment "the reader would ignore what you just
// wrote" becomes an exit 2 instead of a note in some later lint. Warnings are
// compared with line numbers stripped, since an insertion shifts every line
// below it.
func guardConfigRegression(path string, oldData, newData []byte, warnings func([]byte, string) ([]string, error)) error {
	oldWarn, _ := warnings(oldData, path)
	newWarn, err := warnings(newData, path)
	if err != nil {
		return core.Internalf("config", "the edit produced a document that no longer parses (%v) — nothing was written; report this", err)
	}
	old := map[string]int{}
	for _, w := range oldWarn {
		old[stripLineNumbers(w)]++
	}
	var gained []string
	for _, w := range newWarn {
		n := stripLineNumbers(w)
		if old[n] > 0 {
			old[n]--
			continue
		}
		gained = append(gained, w)
	}
	if len(gained) > 0 {
		return core.Validationf("config", "the reader would clamp this value away — refused, nothing written:\n  %s", strings.Join(gained, "\n  "))
	}
	return nil
}

var lineNumberRe = regexp.MustCompile(`:\d+:`)

func stripLineNumbers(w string) string { return lineNumberRe.ReplaceAllString(w, ":") }

// finishConfigEdit writes the document (skipping a no-op byte-identically, the
// Save discipline) and shapes the envelope.
func finishConfigEdit(path, dotted string, typed any, res config.SetResult) (*ConfigEdit, error) {
	edit := &ConfigEdit{File: path, Key: dotted, After: typed, Changed: []string{}}
	if res.Existed {
		edit.Before = config.ParseTOMLValueText(res.OldText)
	}
	if res.Unchanged {
		return edit, nil
	}
	if err := os.WriteFile(path, []byte(res.Doc), 0o644); err != nil {
		return nil, core.Internalf("config", "write %s: %v", path, err)
	}
	edit.Changed = []string{dotted}
	return edit, nil
}

// resolveUserBoard picks the [[board]] entry a --board ref names, in FILE
// order: exact match on path or a scope wins, else a unique substring over
// both; "" resolves only when one entry exists. A miss or ambiguity is exit 2
// with the entry paths in candidates — the did-you-mean contract.
func resolveUserBoard(entries []config.UserBoardEntry, ref, path string) (int, error) {
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	if len(entries) == 0 {
		return 0, core.Validationf("config", "%s declares no [[board]] entry — add one first (furrow config init)", path)
	}
	if ref == "" {
		if len(entries) == 1 {
			return 0, nil
		}
		return 0, &core.Error{
			Code: core.CodeValidation, Kind: core.KindValidation, Subject: "config",
			Msg:        fmt.Sprintf("%d [[board]] entries — pick one with --board <ref>", len(entries)),
			Candidates: paths,
		}
	}
	var hits []int
	for i, e := range entries {
		if e.Path == ref {
			return i, nil
		}
		for _, s := range e.Scopes {
			if s == ref {
				return i, nil
			}
		}
		if strings.Contains(e.Path, ref) {
			hits = append(hits, i)
			continue
		}
		for _, s := range e.Scopes {
			if strings.Contains(s, ref) {
				hits = append(hits, i)
				break
			}
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return 0, &core.Error{
			Code: core.CodeValidation, Kind: core.KindValidation, Subject: "config",
			Msg:        fmt.Sprintf("--board %q matches no [[board]] entry (by path or scope)", ref),
			Candidates: paths,
		}
	default:
		var amb []string
		for _, i := range hits {
			amb = append(amb, entries[i].Path)
		}
		return 0, &core.Error{
			Code: core.CodeValidation, Kind: core.KindValidation, Subject: "config",
			Msg:        fmt.Sprintf("--board %q is ambiguous", ref),
			Candidates: amb,
		}
	}
}

func unknownConfigKeyErr(dotted string, keys []config.Key) *core.Error {
	return &core.Error{
		Code: core.CodeValidation, Kind: core.KindValidation, Subject: "config",
		Msg:        fmt.Sprintf("unknown config key %q", dotted),
		Candidates: config.KeyVocabulary(keys),
	}
}
