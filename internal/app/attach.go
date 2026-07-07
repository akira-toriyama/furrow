package app

import "github.com/akira-toriyama/furrow/internal/core"

// AttachResult reports the outcome of attaching a media file to a task, so the
// CLI can echo the stored path/reference back to the caller (agents want the
// path they can view and the exact body line added).
type AttachResult struct {
	ID   string // task the asset was attached to
	Name string // stored asset basename, e.g. "t-0042-shot.png"
	Path string // store-relative path, e.g. "bodies/assets/t-0042-shot.png"
	Ref  string // body-relative markdown reference, e.g. "assets/t-0042-shot.png"
	Line string // the markdown line appended to the body
}

// Attach copies a media file (srcName + its bytes) into the task's asset area
// (bodies/assets/<id>-*) and appends a markdown reference line to the task's
// body. The id must exist. It is LFS-independent: a plain file copy plus a body
// edit, so git-lfs (if configured via .gitattributes) transparently handles the
// blob and furrow needs no LFS awareness.
//
// The id is validated before anything is written, so a bad id fails cleanly
// (exit 1) and never leaves a stray asset. Reuses AppendBody, so the reference
// line lands idempotently and the body keeps its trailing-newline invariant.
func (a *App) Attach(id, srcName string, data []byte) (*AttachResult, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	if !idx.Has(id) {
		return nil, core.NotFound(id)
	}
	name, err := a.Store.SaveAsset(id, srcName, data)
	if err != nil {
		return nil, err
	}
	ref := core.AssetRef(name)
	line := core.AttachLine(srcName, ref)
	if _, err := a.AppendBody(id, line); err != nil {
		return nil, err
	}
	return &AttachResult{
		ID:   id,
		Name: name,
		Path: core.AssetPath(name),
		Ref:  ref,
		Line: line,
	}, nil
}
