package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// Asset transport for the entity moves and removals — archive, unarchive, rm,
// epic rm — in ONE rule (t-7hhb), after the "<id>-" ownership prefix alone had
// let archive carry off, and rm delete, a file another live body was showing:
//
//   - An asset is IN PLAY when a leaving entity OWNS it (its "<id>-" prefix)
//     or a leaving body REFERENCES it (an assets/<name> link).
//   - It is HELD in the source store when a REMAINING body there references
//     it, or a REMAINING entity there owns it.
//   - A move copies every in-play asset the source has into the destination,
//     then the source loses only what nothing remaining holds. A removal has
//     no destination: it deletes what nothing remaining holds and KEEPS the
//     rest, disclosed in its report.
//
// Ownership never decides a deletion on its own, and no reaper is needed: the
// hot copy a predecessor's occurrence still points at is reaped by the move or
// removal of that LAST holder, because the occurrence's body lists it in play.
// A store that has the file twice (hot and archive/) costs git one tree entry,
// not a second blob — identical bytes are one object.

// AssetTransfer is the asset half of a move or removal report.
type AssetTransfer struct {
	Copied  []string    `json:"copied"`  // landed in the destination store (a move; [] on a removal)
	Deleted []string    `json:"deleted"` // removed from the source store
	Kept    []KeptAsset `json:"kept"`    // left in the source: something remaining there still holds them
}

// KeptAsset is one in-play asset the source store kept, and why.
type KeptAsset struct {
	Name   string   `json:"name"`
	HeldBy []string `json:"held_by"` // remaining ids: a body referencing it, or the entity owning it
}

func newAssetTransfer() AssetTransfer {
	return AssetTransfer{Copied: []string{}, Deleted: []string{}, Kept: []KeptAsset{}}
}

// assetSide is the sliver of a store the transport reads and writes — the hot
// store through app.Store and the archive/ store as fsstore both satisfy it.
type assetSide interface {
	ListAssets() ([]core.AssetInfo, error)
	LoadAsset(name string) ([]byte, error)
	SaveAssetRaw(name string, data []byte) error
	DeleteAsset(name string) error
	ListBodyIDs() ([]string, error)
	LoadBody(id string) (string, error)
}

// assetPlan is the read-only half, computed from the source as it stands
// BEFORE any write: the in-play assets that are on disk there, and for each the
// remaining holders (none = the source may let it go).
type assetPlan struct {
	inPlay []string            // sorted, on disk in the source
	heldBy map[string][]string // name → sorted remaining holder ids
}

// planAssets reads the source once. leaving is the id set of every entity the
// operation takes out of src (tasks, or a box); remaining is the id set of
// every entity still filed there afterwards (the other tasks and every box, or
// on the archive side its own). A body in neither set — an orphan body — still
// holds what it references: nothing furrow removes is ever what some prose in
// the store is showing.
func planAssets(src assetSide, leaving, remaining map[string]bool) (*assetPlan, error) {
	assets, err := src.ListAssets()
	if err != nil {
		return nil, err
	}
	onDisk := make(map[string]bool, len(assets))
	for _, as := range assets {
		onDisk[as.Name] = true
	}
	inPlay := map[string]bool{}
	heldBy := map[string][]string{}
	for _, as := range assets {
		owner := assetOwnerIn(as.Name, leaving, remaining)
		switch {
		case leaving[owner]:
			inPlay[as.Name] = true
		case owner != "":
			heldBy[as.Name] = append(heldBy[as.Name], owner)
		}
	}
	bodyIDs, err := src.ListBodyIDs()
	if err != nil {
		return nil, err
	}
	for _, bid := range bodyIDs {
		body, err := src.LoadBody(bid)
		if err != nil {
			return nil, err
		}
		for _, name := range core.ExtractAssetRefs(body) {
			if leaving[bid] {
				if onDisk[name] { // a ref the source cannot serve has nothing to carry
					inPlay[name] = true
				}
				continue
			}
			heldBy[name] = append(heldBy[name], bid)
		}
	}
	p := &assetPlan{inPlay: []string{}, heldBy: map[string][]string{}} // [] not nil: it is a JSON array on a preview
	for name := range inPlay {
		p.inPlay = append(p.inPlay, name)
		if ids := heldBy[name]; len(ids) > 0 {
			sort.Strings(ids)
			p.heldBy[name] = slices.Compact(ids)
		}
	}
	sort.Strings(p.inPlay)
	return p, nil
}

// assetOwnerIn returns the id among the two sets whose "<id>-" prefix names
// the asset, or "" (an ownerless leftover). Frozen ids are never one another's
// "<id>-" prefix, so at most one matches.
func assetOwnerIn(name string, sets ...map[string]bool) string {
	for _, set := range sets {
		for id := range set {
			if strings.HasPrefix(name, id+"-") {
				return id
			}
		}
	}
	return ""
}

// outcome splits the plan into what the source will lose and what it keeps —
// the preview's answer, and exactly what reap then does.
func (p *assetPlan) outcome() (deleted []string, kept []KeptAsset) {
	deleted, kept = []string{}, []KeptAsset{}
	for _, name := range p.inPlay {
		if ids := p.heldBy[name]; len(ids) > 0 {
			kept = append(kept, KeptAsset{Name: name, HeldBy: ids})
			continue
		}
		deleted = append(deleted, name)
	}
	return deleted, kept
}

// copyAssets lands every in-play asset in dst under its exact name. A copy dst
// already has is the same file — a retry after an interrupted move, or the
// copy a still-held asset earned from an earlier move of another holder — so
// the overwrite changes nothing but a hand edit, a text file's line endings,
// or a git-lfs pointer a checkout left in dst, which the file replaces:
// refuseAssetClash has already refused a pair whose owner id both stores hold
// with other content. One route still writes over a different
// file: an owner in both stores whose board copy re-attached under a name
// archive/ holds, then removed with rm while another body keeps the file,
// leaves that name to an owner in one store (kept by t-erd6; attach skipping
// the names archive/ holds would close it at the source). One write is never
// made: a source copy that is only the git-lfs pointer for the file dst holds,
// line endings aside (lfsPointerFor), leaves dst's file in place, since writing
// it would swap the file for its pointer — a different blob wherever dst's
// path is not LFS-tracked, and a working tree without the bytes where it is.
func copyAssets(src, dst assetSide, p *assetPlan) ([]string, error) {
	copied := []string{}
	for _, name := range p.inPlay {
		data, err := src.LoadAsset(name)
		if err != nil {
			return nil, err
		}
		keep, err := lfsPointerFor(dst, name, data)
		if err != nil {
			return nil, err
		}
		if !keep {
			if err := dst.SaveAssetRaw(name, data); err != nil {
				return nil, err
			}
		}
		copied = append(copied, name)
	}
	return copied, nil
}

// lfsPointerFor reports whether data is only the git-lfs pointer for the file
// dst already holds under name (pointerOf). dst is read only when data has a
// pointer's shape, so a move of real files costs no second read.
func lfsPointerFor(dst assetSide, name string, data []byte) (bool, error) {
	if len(data) >= 1024 || !bytes.HasPrefix(foldCRLF(data), []byte(lfsPointerVersion)) {
		return false, nil
	}
	have, err := dst.LoadAsset(name)
	if fe := core.AsError(err); fe != nil && fe.Code == core.CodeNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return pointerOf(data, have), nil
}

// sharedIDs maps each id both indexes hold as loaded to whether it is one
// task (core.SameTask), an interrupted move's, rather than two tasks under
// one id. Membership is the key: a false value is still a shared id.
func sharedIDs(hot, arc *core.Index) map[string]bool {
	inArc := make(map[string]*core.Task, len(arc.Tasks))
	for i := range arc.Tasks {
		inArc[arc.Tasks[i].ID] = &arc.Tasks[i]
	}
	shared := map[string]bool{}
	for i := range hot.Tasks {
		if held := inArc[hot.Tasks[i].ID]; held != nil {
			shared[hot.Tasks[i].ID] = core.SameTask(&hot.Tasks[i], held)
		}
	}
	return shared
}

// refuseAssetClash refuses a move before it writes anything when an in-play
// asset whose owner (its "<id>-" prefix) is in shared, the ids both stores
// hold, already sits in dst with other content. Attach is a name's only
// writer, and it runs on the board, never reusing a name the board has: while
// the owner is in one store, an asset on both sides is one file twice — bar a
// hand edit and the one route copyAssets names — and a move keeps overwriting
// such a pair as it always did, but for the git-lfs pointer copyAssets never
// writes over its file. With the owner in both stores, attach on the
// board's copy can reuse a name archive/ still holds, and any move that
// carries the file would write one over the other: the owner's own, or that
// of another task whose body shows it. The way out the refusal names differs:
// one task keeps both files once the board's copy takes another name, while
// two tasks under one id need the id repair first, which renames the re-id'd
// task's files — a repair that left them under the old prefix would take its
// id out of shared and the guard with it.
func refuseAssetClash(src, dst assetSide, p *assetPlan, shared map[string]bool, verb string) error {
	var clash []string
	for _, name := range p.inPlay {
		if assetOwnerIn(name, shared) == "" {
			continue
		}
		have, err := dst.LoadAsset(name)
		if fe := core.AsError(err); fe != nil && fe.Code == core.CodeNotFound {
			continue
		}
		if err != nil {
			return err
		}
		want, err := src.LoadAsset(name)
		if err != nil {
			return err
		}
		if !sameAsset(have, want) {
			clash = append(clash, name)
		}
	}
	if len(clash) == 0 {
		return nil
	}
	return clashRefusal(clash, shared, verb)
}

// clashRefusal words refuseAssetClash's refusal for the clashing names: the
// one-task way out for a name whose owner is one task in both stores, the id
// repair for one whose owner id names two tasks, and both when one call meets
// both — the one-task advice then naming its files, the id repair its id — a
// refusal that gave one would refuse the re-run again with the other.
func clashRefusal(clash []string, shared map[string]bool, verb string) error {
	paths := make([]string, len(clash))
	var single []string   // paths whose owner is one task in both stores
	var twoTasks []string // owner ids that name two tasks, in clash order
	for i, name := range clash {
		paths[i] = core.AssetPath(name)
		switch owner := assetOwnerIn(name, shared); {
		case shared[owner]:
			single = append(single, paths[i])
		case !slices.Contains(twoTasks, owner):
			twoTasks = append(twoTasks, owner)
		}
	}
	what := paths[0] + " differs between the board and archive/ under one name — the move would write one copy over the other"
	if len(clash) > 1 {
		what = strings.Join(paths, ", ") + " differ between the board and archive/, each under one name — the move would write one copy of each over the other"
	}
	var how []string
	if len(single) > 0 {
		h := "rename the board's copy by hand and point each board link to it at the new name, keeping the old link beside it in the body of a task that sits in both stores (a finished move refuses a body that drops a line)"
		if len(single) > 1 {
			h = "rename the board's copies by hand and point each board link to one at its new name, keeping the old link beside it in the body of a task that sits in both stores (a finished move refuses a body that drops a line)"
		}
		if len(twoTasks) > 0 {
			h = "for " + strings.Join(single, ", ") + ", " + h
		}
		how = append(how, h)
	}
	switch {
	case len(twoTasks) == 1:
		how = append(how, twoTasks[0]+" names two tasks (`furrow lint` flags it as duplicate-id): "+reIDRepair)
	case len(twoTasks) > 1:
		how = append(how, strings.Join(twoTasks, ", ")+" each name two tasks (`furrow lint` flags them as duplicate-id): for each, "+reIDRepair)
	}
	return core.Validationf(clash[0], "%s, so nothing was %s; %s, then re-run", what, verb, strings.Join(how, "; and "))
}

// sameAsset reports whether two copies of an asset count as one content for
// the clash check: byte for byte, but for two differences furrow did not make.
// Git's line-ending conversion (core.autocrlf) can check one copy out with
// CRLF and leave furrow's write of the other with LF, so a copy with no NUL
// byte — text, not an image — is compared with CRLF folded to LF; the fold
// does not read git's config, so where git converts nothing the move may
// still change a file's line endings. And a checkout without the file can
// leave one copy the git-lfs pointer for the file the other holds (pointerOf),
// which copyAssets never writes over that file.
func sameAsset(a, b []byte) bool {
	if bytes.Equal(a, b) || pointerOf(a, b) || pointerOf(b, a) {
		return true
	}
	if bytes.IndexByte(a, 0) >= 0 || bytes.IndexByte(b, 0) >= 0 {
		return false
	}
	return equalFolded(a, b)
}

// equalFolded reports whether a and b are equal with every CRLF folded to LF,
// as foldCRLF would fold them, without building either folded copy: the clash
// check already holds both copies of what may be a large file.
func equalFolded(a, b []byte) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i] == '\r' && i+1 < len(a) && a[i+1] == '\n' {
			i++
		}
		if b[j] == '\r' && j+1 < len(b) && b[j+1] == '\n' {
			j++
		}
		if a[i] != b[j] {
			return false
		}
		i++
		j++
	}
	return i == len(a) && j == len(b)
}

// foldCRLF is b with every CRLF folded to LF.
func foldCRLF(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// pointerOf reports whether ptr is the git-lfs pointer for data (pointsTo),
// line endings aside. git-lfs keeps a file's bytes as written (-text), while
// at a path git-lfs does not track, core.autocrlf or a text attribute
// converts a copy: the text file a pointer names can sit there with CRLF
// where the pointer names LF, or normalized to LF where it names CRLF, and a
// pointer committed there as content can come out with CRLF. So ptr is tried
// with CRLF folded, against data as it is, with CRLF folded, and with every
// LF then expanded to CRLF. That undoes git's normalization only where every
// line ends in one LF or one CRLF: a file with uneven line endings — mixed, or
// more than one CR before an LF — that git normalized on one side is not
// recognized, and the bytes decide. The
// variants are hashed as they stream (lineSum), never built: data is the
// other copy of what may be a large file, and the pointer's size line rules
// out a variant before any hashing.
func pointerOf(ptr, data []byte) bool {
	if len(ptr) >= 1024 {
		return false
	}
	p := foldCRLF(ptr)
	if pointsTo(p, data) {
		return true
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	folded := len(data) - bytes.Count(data, []byte("\r\n"))
	return pointerNames(p, folded, func() []byte { return lineSum(data, false) }) ||
		pointerNames(p, folded+bytes.Count(data, []byte("\n")), func() []byte { return lineSum(data, true) })
}

// lineSum is the sha256 of data with every CRLF folded to LF and, with crlf,
// every LF then written as CRLF — what foldCRLF, and the expansion after it,
// would build, hashed line by line instead.
func lineSum(data []byte, crlf bool) []byte {
	eol := []byte("\n")
	if crlf {
		eol = []byte("\r\n")
	}
	h := sha256.New()
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			h.Write(data)
			return h.Sum(nil)
		}
		h.Write(bytes.TrimSuffix(data[:i], []byte("\r")))
		h.Write(eol)
		data = data[i+1:]
	}
}

// lfsPointerVersion opens every git-lfs pointer file (spec v1).
const lfsPointerVersion = "version https://git-lfs.github.com/spec/v1\n"

// pointsTo reports whether ptr is exactly the pointer git-lfs writes for data:
// the version line, then data's sha256 oid and its size — the bytes a
// checkout through git-lfs leaves in the working tree without the file
// (re-encoding even a non-canonical pointer blob) and `git lfs pointer
// --file` prints. Other pointer-like text was written by hand, by another
// tool, or left by a checkout without git-lfs: git-lfs stores a valid
// non-canonical pointer verbatim, a different blob it still smudges to the
// file, and malformed text as content, so it is no pointer here and the bytes
// decide.
func pointsTo(ptr, data []byte) bool {
	return pointerNames(ptr, len(data), func() []byte {
		sum := sha256.Sum256(data)
		return sum[:]
	})
}

// pointerNames reports whether ptr is the pointer git-lfs writes for content
// of size bytes whose sha256 sum returns; sum runs only once ptr's shape and
// size line match.
func pointerNames(ptr []byte, size int, sum func() []byte) bool {
	tail := "size " + strconv.Itoa(size) + "\n"
	if len(ptr) >= 1024 || !bytes.HasPrefix(ptr, []byte(lfsPointerVersion)) || !bytes.HasSuffix(ptr, []byte(tail)) {
		return false
	}
	return string(ptr) == lfsPointerVersion+"oid sha256:"+hex.EncodeToString(sum())+"\n"+tail
}

// reapAssets applies the plan's outcome to the source: deletes what nothing
// remaining holds, keeps the rest. Run only after the destination (if any) and
// both indexes are durable.
func reapAssets(src assetSide, p *assetPlan) ([]string, []KeptAsset, error) {
	deleted, kept := p.outcome()
	for _, name := range deleted {
		if err := src.DeleteAsset(name); err != nil {
			return nil, nil, err
		}
	}
	return deleted, kept, nil
}

// remainingIDs is the id set of every entity still in the store after ids
// leave: the index's other tasks plus every box (a box owns nothing today —
// attach takes a task id — but a hand-placed "e-…-" file is its to hold).
func remainingIDs(idx *core.Index, epics []core.Epic, leaving map[string]bool) map[string]bool {
	out := make(map[string]bool, len(idx.Tasks)+len(epics))
	for i := range idx.Tasks {
		if !leaving[idx.Tasks[i].ID] {
			out[idx.Tasks[i].ID] = true
		}
	}
	for i := range epics {
		if !leaving[epics[i].ID] {
			out[epics[i].ID] = true
		}
	}
	return out
}
