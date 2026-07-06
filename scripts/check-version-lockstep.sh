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
  echo "so 'nix run/install' reports the real release version (not a stale one)." >&2
  exit 1
fi

echo "ok — nix flake version matches the release pin ($flake_ver)"
