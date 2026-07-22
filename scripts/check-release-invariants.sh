#!/bin/sh
# check-release-invariants.sh — pin the release-config invariants that YAML/shell
# cannot express, so a one-token regression can't ship SILENTLY (green CI, broken
# tag). furrow already asserts the release ARTIFACT SHAPE (check-release-artifacts
# .sh); this guards the release CONFIG/BEHAVIOR. Pure text-extraction — no
# goreleaser, no toolchain — so it always runs (unlike the artifact dry-run).
set -eu
cd "$(dirname "$0")/.."

fail=0
note() { printf 'check-release-invariants: %s\n' "$1" >&2; fail=1; }

# (1) ldflags -X symbol path. The build stamps
# <module>/internal/version.{Version,Commit,Date} from THREE places
# (.goreleaser.yaml, build.sh, flake.nix). Go's `-X` does NOT warn on an unknown
# symbol, so if the package moves or a var is renamed those all no-op and every
# release / nix / install ships Version="dev". A grep of the configs alone can't
# see that; assert (a) each config still names the path, (b) the path resolves to
# the REAL package+vars, (c) each config stamps the expected vars.
module="$(awk '/^module /{print $2; exit}' go.mod)"
verpkg="$module/internal/version"

# (1a) the version-package path is named in every build config (build.sh names it
# once as PKG=… then uses ${PKG}; the others inline it).
for f in .goreleaser.yaml build.sh flake.nix; do
	grep -q "$verpkg" "$f" || note "$f no longer names the ldflags target path $verpkg (package moved?)"
done

# (1b) that path resolves to a real package declaring the stamped vars, so a
# rename of EITHER side (config or code) is caught — the check a grep of the
# configs alone cannot make.
dir="internal/version"
if [ ! -d "$dir" ]; then
	note "ldflags target $verpkg has no package dir $dir/ (package moved — every -X would no-op)"
else
	grep -rqs '^package version' "$dir"/*.go || note "$dir/ does not declare 'package version'"
	for v in Version Commit Date; do
		grep -rqs "^[[:space:]]*$v = " "$dir"/*.go || note "$dir/ does not declare var $v (ldflags -X $verpkg.$v would no-op)"
	done
fi

# (1c) each config actually stamps the vars. Form-agnostic: an -X sets
# "<path>.<Var>=<value>", so ".<Var>=" is present whether the path is inlined
# (`version.Version=`) or via a variable (`${PKG}.Version=`). flake.nix stamps
# Version+Commit only (nix has no commit-date input; Resolve backfills Date).
for v in Version Commit Date; do
	grep -q "\.${v}=" .goreleaser.yaml || note ".goreleaser.yaml does not stamp $v via -X"
	grep -q "\.${v}=" build.sh || note "build.sh does not stamp $v via -X"
done
for v in Version Commit; do
	grep -q "\.${v}=" flake.nix || note "flake.nix does not stamp $v via -X"
done

# (2) GoReleaser publishes at tag time, NOT as a draft: a draft's assets are 404
# to anonymous clients while update-tap pushes the cask in the same run (cifail's
# 17-day brew-404 window). `draft: true` is a one-token regression that only
# surfaces after the tag ships.
grep -Eq '^[[:space:]]*draft:[[:space:]]*false' .goreleaser.yaml || note ".goreleaser.yaml release.draft must be explicitly false"
if grep -Eq '^[[:space:]]*draft:[[:space:]]*true' .goreleaser.yaml; then
	note ".goreleaser.yaml sets draft: true — assets 404 during the same-run tap push"
fi

# (3) release.yml folds glyph notes' soft "nothing release-worthy" exit (1) into
# an empty-notes release, rather than letting `set -e` abort the already-tagged
# job. Losing that branch turns a benign no-release verdict into a failed release.
grep -qF '"$status" -eq 1' .github/workflows/release.yml || note "release.yml lost the soft exit-1 fold for 'glyph notes' (a no-release verdict would abort the tagged job)"

if [ "$fail" -ne 0 ]; then
	printf '  release-config invariants drifted — a regression here ships silently (green CI, broken tag).\n' >&2
	exit 1
fi
echo "ok — release-config invariants hold (ldflags path -> real package, draft:false, soft exit-1 fold)"
