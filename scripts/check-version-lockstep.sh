#!/bin/sh
# check-version-lockstep.sh — guard the nix flake version ⇄ release-pin lockstep.
# A flake has no tag info at eval time, so flake.nix hardcodes a `version` string;
# if it is not bumped with each release, `nix run/install` reports a stale version
# (audit F9: it sat at "0.1.0-dev" through the whole v0.6.x line). The authoritative
# "current release" pin already maintained per release is sync-task-status.yml's
# `furrow-version` default (bumped right before tagging, per that file's own
# lockstep rule). This asserts flake.nix's version == that pin (minus the leading
# v), with no git-tag or network dependency (pure text extraction — deterministic,
# same shape as check-readme-parity.sh).
set -eu
cd "$(dirname "$0")/.."

# flake.nix: the sole `version = "X";` assignment (`inherit version;` and the meta
# homepage do not match this `= "…"` form). Capture the FULL quoted string so a
# pre-release/build suffix survives (compared like-for-like with the pin below).
flake_ver="$(sed -n 's/^[[:space:]]*version = "\([^"]*\)";.*/\1/p' flake.nix | head -1)"

# sync-task-status.yml furrow-version default. Anchored to the 8-space input-default
# indent and requiring a v<digit> value: this is the only `default:` shaped that way
# (sibling inputs default to bare words), and the description prose — indented deeper
# (10 spaces) — can never match, so rewording it can't fool the guard. Capture
# everything after the `v` (not just [0-9.]) so an -rc/+meta suffix survives too,
# matching flake_ver's normalization exactly (else an identical -rc pin false-fails).
pin_ver="$(sed -n 's/^        default:[[:space:]]*v\([0-9][^[:space:]]*\).*/\1/p' \
  .github/workflows/sync-task-status.yml | head -1)"

if [ -z "$flake_ver" ] || [ -z "$pin_ver" ]; then
  echo "✖ could not extract both versions:" >&2
  echo "  flake.nix version:               ${flake_ver:-<none>}" >&2
  echo "  sync-task-status furrow-version: ${pin_ver:-<none>}" >&2
  exit 1
fi

if [ "$flake_ver" != "$pin_ver" ]; then
  echo "✖ nix flake version is out of lockstep with the release pin:" >&2
  echo "  flake.nix:                       $flake_ver" >&2
  echo "  sync-task-status furrow-version: $pin_ver" >&2
  echo >&2
  echo "Bump flake.nix's version to match right before tagging (release-prep)," >&2
  echo "so 'nix run/install' reports the real version (not a stale one)." >&2
  exit 1
fi

echo "ok — nix flake version matches the release pin ($flake_ver)"

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
