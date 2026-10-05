#!/bin/sh
# check-version-lockstep.sh [<tag>] — guard the version literals a release bumps.
#
# flake.nix's `version` is THE release pin: a flake has no tag info at eval time,
# so it is the one version this repo must write down by hand (audit F9: it sat at
# "0.1.0-dev" through the whole v0.6.x line). Everything else either derives from
# the tag or is compared against this pin (README's `uses:` example —
# check-readme-parity.sh).
#
# Three checks, pure text extraction (no git-tag or network dependency):
#   1. sync-task-status.yml carries NO concrete `furrow-version` default. It
#      derives the binary version from the tag the caller pinned (job.workflow_ref);
#      the hand-bumped default it replaced shipped v4.0.0 inside the v5.0.0 and
#      v5.1.0 tags, and nothing compared it to the tag.
#   2. with <tag> (release.yml passes the pushed tag): the tag IS v<pin>. At v5.0.0
#      and v5.1.0 this pin and README's were still 4.0.0 — release-prep had been
#      skipped and the release shipped anyway. It runs before GoReleaser, so a
#      skipped release-prep publishes nothing.
#   3. flake.nix's vendorHash was re-pinned after the last go.sum change. It
#      gates the release too, since release.yml runs this whole script: a tag
#      whose `nix build` is already broken does not ship.
set -eu
cd "$(dirname "$0")/.."
tag="${1:-}"

# Asserted POSITIVELY — the furrow-version input's default line must be exactly
# `default: ""` — because a search for something version-shaped is passed by
# every spelling it did not think of (single quotes, no leading v), and any
# non-empty default silently pins every caller to it. The block runs from the
# input's own line to the next key at its indent or shallower.
default_line="$(awk '
  /^      furrow-version:/ { f = 1; next }
  f && (/^      [^ ]/ || /^    [^ ]/) { exit }
  f && /^        default:/ { print; exit }
' .github/workflows/sync-task-status.yml)"
if [ "$default_line" != '        default: ""' ]; then
  echo "✖ sync-task-status.yml's furrow-version default is not the empty string:" >&2
  echo "  found: ${default_line:-<no default line in the furrow-version input>}" >&2
  echo >&2
  echo "The reusable derives its binary version from the tag the caller pinned" >&2
  echo "(job.workflow_ref). A literal default is a second copy of that tag which" >&2
  echo "nothing bumps: keep it \"\" — the input is only an override for a non-tag pin." >&2
  exit 1
fi
echo "ok — sync-task-status.yml derives its version from the pinned tag (no literal default)"

# flake.nix: the sole `version = "X";` assignment (`inherit version;` and the meta
# homepage do not match this `= "…"` form). Capture the FULL quoted string so a
# pre-release/build suffix survives (compared like-for-like with the tag below).
flake_ver="$(sed -n 's/^[[:space:]]*version = "\([^"]*\)";.*/\1/p' flake.nix | head -1)"
if [ -z "$flake_ver" ]; then
  echo "✖ could not extract flake.nix's version (the release pin)" >&2
  exit 1
fi

if [ -n "$tag" ]; then
  if [ "$tag" != "v$flake_ver" ]; then
    echo "✖ the tag being released is not the release pin:" >&2
    echo "  tag:               $tag" >&2
    echo "  flake.nix version: $flake_ver" >&2
    echo >&2
    echo "release-prep was skipped: on main, bump flake.nix's version to ${tag#v} and" >&2
    echo "README's sync-task-status.yml@ pin to $tag, then move the tag to that commit" >&2
    echo "and push it again. Nothing has been published." >&2
    exit 1
  fi
  echo "ok — tag $tag is the release pin"
else
  echo "ok — release pin is $flake_ver (flake.nix)"
fi

# vendorHash freshness. A flake cannot re-derive the vendored-module hash at
# eval time, so flake.nix carries a stamp of the go.sum its vendorHash was
# computed against; when go.mod/go.sum change and the re-pin ritual (fakeHash →
# nix build → paste) is skipped, every `nix build`/`nix run` is already failing
# with a fixed-output hash mismatch — this catches it at check/CI time, with no
# nix on the runner (#127 shipped exactly that breakage: it dropped every charm
# module from go.sum and left the pre-removal vendorHash in place).
stamp="$(sed -n 's/^[[:space:]]*# go\.sum sha256: \([0-9a-f]\{64\}\).*/\1/p' flake.nix | head -1)"
if command -v shasum >/dev/null 2>&1; then
  gosum="$(shasum -a 256 go.sum | cut -d' ' -f1)"
else
  gosum="$(sha256sum go.sum | cut -d' ' -f1)"
fi

if [ -z "$stamp" ]; then
  echo "✖ flake.nix carries no '# go.sum sha256: <hash>' stamp — the vendorHash" >&2
  echo "  freshness guard has nothing to compare. Re-add the stamp line:" >&2
  echo "  # go.sum sha256: $gosum" >&2
  exit 1
fi

if [ "$stamp" != "$gosum" ]; then
  echo "✖ go.sum changed but flake.nix's vendorHash was not re-pinned:" >&2
  echo "  stamped go.sum sha256: $stamp" >&2
  echo "  actual  go.sum sha256: $gosum" >&2
  echo >&2
  echo "nix build is broken right now (fixed-output hash mismatch). Re-pin:" >&2
  echo "  1. set vendorHash = pkgs.lib.fakeHash in flake.nix" >&2
  echo "  2. nix build .#   # copy the 'got: sha256-...' hash into vendorHash" >&2
  echo "  3. update the '# go.sum sha256:' stamp to $gosum" >&2
  exit 1
fi

echo "ok — flake.nix vendorHash stamp matches go.sum (re-pin ritual not skipped)"
