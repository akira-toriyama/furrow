package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/akira-toriyama/furrow/internal/config"
)

// BoardsList is what `furrow boards` prints: the user-level config file and one
// entry per configured [[board]], in file (declaration) order — the
// machine-wide answer to "which boards does this machine know about",
// independent of cwd. Config names the FILE READ whether or not it exists;
// Boards is [] (never null) when the file is missing or holds no usable
// [[board]] — that emptiness IS the diagnosis (a machine with furrow installed
// but no scopes configured), so the command exits 0 on it.
type BoardsList struct {
	Config string       `json:"config"` // the user-level config path furrow reads
	Boards []BoardEntry `json:"boards"` // [] when the file is missing or has no usable entry
}

// BoardEntry is one configured board. Store and Scopes are resolved to absolute
// paths (~ expanded, relative paths anchored at the config file's directory —
// mechanical normalization, still cwd-independent); Repo and Label stay as
// DECLARED, because "auto" cannot resolve to an owner/repo without a checkout
// to derive it from. The vocabulary and schema keys are `furrow board`'s own
// (shared embedded structs), so one parser reads both views. When Exists is
// false the vocabulary is EMPTY, not defaults — furrow reports what it read,
// never what it guesses — and the schema triple reads unreadable; Exists tells
// a missing board apart from a present-but-corrupt one.
type BoardEntry struct {
	Store  string   `json:"store"`  // absolute .furrow path, as configured
	Scopes []string `json:"scopes"` // directories this board activates under, resolved
	Repo   string   `json:"repo"`   // declared scope repo: "auto" | "" | owner/repo
	Label  string   `json:"label"`  // declared literal add-time tag ("" = none)
	Exists bool     `json:"exists"` // the store directory is present on disk
	BoardVocab
	SchemaTriple
}

// Boards lists the machine's configured boards WITHOUT resolving any of them
// against cwd — it answers (exit 0) exactly where every other command exits 2
// because no board is in scope, which is the point: it is the diagnosis call,
// and the one-call bootstrap a GUI front-end (cwd=/) needs to find the central
// board without re-parsing furrow's config format itself. It reads the
// user-level config FILE only: the FURROW_BOARD env override is a
// per-invocation redirect, not machine configuration, so it is deliberately
// not listed. Clamp warnings (a path-less or scope-less [[board]], an
// unresolvable path) surface in the second return for the CLI to note on
// stderr — clamp-don't-reject, same as discovery.
func Boards() (*BoardsList, []string, error) {
	path, err := globalConfigPath()
	if err != nil {
		return nil, nil, err
	}
	entries, warn, err := config.LoadGlobalBoards(path)
	if err != nil {
		return nil, warn, err
	}
	cfgDir := filepath.Dir(path)
	list := &BoardsList{Config: path, Boards: []BoardEntry{}}
	for _, b := range entries {
		store, rerr := resolvePathRelTo(cfgDir, b.Path)
		if rerr != nil {
			warn = append(warn, fmt.Sprintf("ignoring central board %q: %v", b.Path, rerr))
			continue
		}
		scopes := []string{}
		for _, s := range b.Scopes {
			sp, serr := resolvePathRelTo(cfgDir, s)
			if serr != nil {
				warn = append(warn, fmt.Sprintf("board %q: ignoring scope %q: %v", b.Path, s, serr))
				continue
			}
			scopes = append(scopes, sp)
		}
		list.Boards = append(list.Boards, probeBoardEntry(store, scopes, b))
	}
	return list, warn, nil
}

// probeBoardEntry probes one configured board. A missing directory (or one whose
// config.toml cannot even parse) keeps the empty vocabulary and the unreadable
// triple; a present board is opened exactly like discovery would open it
// (config clamp included) and reports the same vocabulary and the same folded
// hot+archive schema state as `furrow board` on that board.
func probeBoardEntry(store string, scopes []string, b config.GlobalBoard) BoardEntry {
	e := BoardEntry{
		Store:        store,
		Scopes:       scopes,
		Repo:         b.Repo,
		Label:        b.Label,
		BoardVocab:   emptyVocab(),
		SchemaTriple: schemaTriple(0, SchemaUnreadable, false),
	}
	if fi, err := os.Stat(store); err != nil || !fi.IsDir() {
		return e
	}
	e.Exists = true
	a, err := openAt(store)
	if err != nil {
		return e
	}
	e.BoardVocab = a.boardVocab()
	e.SchemaTriple = schemaTriple(a.schemaState())
	return e
}

// emptyVocab is the vocabulary of a board that could not be read: empty
// collections (never null — the deterministic-output rule), zero scalars. NOT
// config defaults: reporting defaults for a board we never opened would be a
// guess dressed as a fact.
func emptyVocab() BoardVocab {
	return BoardVocab{
		Lanes:      []string{},
		NextLanes:  []string{},
		Terminal:   []string{},
		Types:      []string{},
		Containers: []string{},
	}
}
