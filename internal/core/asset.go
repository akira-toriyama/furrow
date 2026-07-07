package core

import (
	"regexp"
	"strconv"
	"strings"
)

// Media attachments. furrow stores attached images/video as plain files under
// bodies/assets/<id>-<name> and references them from the task body with a
// relative markdown link. These helpers own the pure naming and rendering
// policy (filesystem- and markdown-safe names, collision-free suffixing, image
// vs. link rendering); the store owns the actual copy and the "is this name
// taken" check. This split keeps the policy testable without a disk and shared
// byte-for-byte between fsstore and memstore.

// AssetInfo is one enumerated on-disk asset: its basename (under bodies/assets/)
// and byte size. The store fills it (only the store touches disk); lint consumes
// it for the orphan (unreferenced) and oversized (over DefaultAssetWarnBytes)
// checks. Size is the plain file size — for a Git LFS-smudged working tree that
// is the real media, not the pointer.
type AssetInfo struct {
	Name string
	Size int64
}

// DefaultAssetWarnBytes is the size at or above which `furrow lint` warns that an
// asset is large (a heuristic nudge to Git-LFS-track or shrink it, since a blob
// committed raw stays in git history forever). 5 MiB comfortably clears masked
// screenshots yet catches raw video and unoptimized captures. It is warn-only, so
// crossing it never fails lint; a configurable threshold is a later refinement.
const DefaultAssetWarnBytes int64 = 5 << 20

// AssetPath returns the store-relative path of an attached asset by basename,
// e.g. "bodies/assets/t-0042-shot.png" — the asset twin of BodyPath. The store
// turns this into an absolute path; callers never hand-assemble it.
func AssetPath(name string) string { return "bodies/assets/" + name }

// AssetRef returns the body-relative reference to an asset by basename, e.g.
// "assets/t-0042-shot.png" — what goes inside the body's markdown link, since
// bodies/<id>.md sits one directory above bodies/assets/.
func AssetRef(name string) string { return "assets/" + name }

// imageExts are the lowercased extensions furrow embeds inline as an image.
var imageExts = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".avif"}

// AttachLine renders the markdown line that references an attached asset from a
// task body. Images are embedded (![alt](ref)); everything else — video, other
// media — is a plain link ([alt](ref)), because GitHub and most renderers do
// not inline video from a relative path.
func AttachLine(alt, ref string) string {
	lower := strings.ToLower(ref)
	for _, ext := range imageExts {
		if strings.HasSuffix(lower, ext) {
			return "![" + alt + "](" + ref + ")"
		}
	}
	return "[" + alt + "](" + ref + ")"
}

// assetRefRe matches a markdown image/link whose target is an attached asset:
// the "](assets/<name>)" tail that both AttachLine forms (![alt](…) and
// [alt](…)) end in. The single capture group is the asset basename, i.e. what
// AssetRef prefixes with "assets/". A leading "./" is tolerated. The class
// stops at ')', whitespace, or a quote so a markdown title ("](assets/x "t")")
// never leaks into the name. Because SanitizeAssetName guarantees the on-disk
// name has no spaces/parens, this loses nothing that attach ever wrote.
var assetRefRe = regexp.MustCompile(`\]\(\s*(?:\./)?assets/([^)\s"']+)`)

// ExtractAssetRefs returns the asset basenames referenced from body markdown via
// an "assets/<name>" link or image target, in first-seen order and de-duplicated
// (the twin of ExtractLinks for [[id]] wiki-links). Code spans and fences are
// stripped first (see stripCode), so an assets/ ref written as a documented
// EXAMPLE inside `backticks` or a ``` fence ``` is not treated as a live
// reference — matching how the dangling-link check ignores documented [[t-…]]
// placeholders. Returns nil when there are no references. lint uses this to find
// orphan assets (on disk, referenced by no body) and dangling refs (a body
// pointing at an asset that is not on disk).
func ExtractAssetRefs(text string) []string {
	ms := assetRefRe.FindAllStringSubmatch(stripCode(text), -1)
	if len(ms) == 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range ms {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// SanitizeAssetName reduces an arbitrary source filename to a safe, portable
// asset basename: any directory component is dropped, every character outside
// [A-Za-z0-9._-] becomes '-', runs of '-' collapse to one, and leading/trailing
// '-'/'.' are trimmed. An input that sanitizes to nothing yields "file". The
// result is used both as the on-disk name and inside the body's markdown link,
// so it must be filesystem- and markdown-safe.
func SanitizeAssetName(src string) string {
	// Drop any directory portion — defense in depth against a caller passing a
	// full path (and against path traversal), independent of OS separator.
	if i := strings.LastIndexAny(src, `/\`); i >= 0 {
		src = src[i+1:]
	}
	var b strings.Builder
	prevDash := false
	for _, r := range src {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "file"
	}
	return out
}

// NextAssetName returns base if taken(base) is false, else base with a numeric
// suffix inserted before the extension (foo.png -> foo-2.png -> foo-3.png …)
// until taken reports a candidate free. The taken predicate lets each store
// probe its own medium (files on disk, map keys in memory) without core knowing
// about either.
func NextAssetName(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	ext := assetExt(base)
	stem := base[:len(base)-len(ext)]
	for n := 2; ; n++ {
		cand := stem + "-" + strconv.Itoa(n) + ext
		if !taken(cand) {
			return cand
		}
	}
}

// assetExt returns the extension of a (separator-free) asset basename — the
// final dot-suffix, or "" when there is none. A leading dot is not an
// extension (".gitignore" has ext ""), matching filepath.Ext, but without
// importing filepath (core stays stdlib-pure of path/os).
func assetExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[i:]
	}
	return ""
}
