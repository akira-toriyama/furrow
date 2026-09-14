package app

import (
	"slices"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
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
// hot copy a predecessor's successor still points at is reaped by the move or
// removal of that LAST holder, because the successor's body lists it in play.
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

// copyAssets lands every in-play asset in dst under its exact name — an
// idempotent overwrite of identical bytes when dst already has it (a retry
// after an interrupted move, or the copy a still-held asset earned from an
// earlier move of another holder).
func copyAssets(src, dst assetSide, p *assetPlan) ([]string, error) {
	copied := []string{}
	for _, name := range p.inPlay {
		data, err := src.LoadAsset(name)
		if err != nil {
			return nil, err
		}
		if err := dst.SaveAssetRaw(name, data); err != nil {
			return nil, err
		}
		copied = append(copied, name)
	}
	return copied, nil
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
