#!/bin/sh
# check-release-artifacts.sh — assert that what GoReleaser actually produced in
# dist/ matches what release.yml is about to DO with it. Both v0.8.0 release
# defects were of exactly this shape: the config was schema-valid (`goreleaser
# check` passed) and the workflow was syntactically fine, but the artifact names
# on disk did not line up with the paths the next step fed to another tool. That
# is invisible until a tag is pushed — by which point GoReleaser has already
# published the draft and pushed the Homebrew cask, so the failure ships.
#
# This runs on every PR against a `--snapshot` build (build.yml) AND inside the
# real release (release.yml), so a config change is exercised before it is tagged.
#
# It also OWNS the version derivation (release.yml consumes it via GITHUB_OUTPUT),
# so the paths asserted here and the paths attested there cannot drift apart.
#
# Usage: sh scripts/check-release-artifacts.sh [dist-dir]
set -eu
cd "$(dirname "$0")/.."
dist="${1:-dist}"

[ -d "$dist" ] || { echo "✖ no such directory: $dist (run goreleaser first)" >&2; exit 1; }

# The version as it appears IN THE FILENAMES — not from git. In a snapshot build
# it is something like 0.8.1-SNAPSHOT-abc1234; at release it is 0.8.0. Deriving it
# from the artifact that actually exists is the whole point: it is what makes the
# concrete paths below concrete.
f="$(ls "$dist"/furrow_*_linux_amd64.tar.gz 2>/dev/null | head -1 || true)"
[ -n "$f" ] || { echo "✖ no furrow_*_linux_amd64.tar.gz in $dist/ — did the build pipe run?" >&2; exit 1; }
v="${f#"$dist"/furrow_}"
v="${v%_linux_amd64.tar.gz}"
echo "release artifacts: version=$v"

fail=0
note() { echo "✖ $*" >&2; fail=1; }

# ---------------------------------------------------------------------------
# 1. Every path release.yml hands to the attest action must resolve to a REAL
#    file — checked as the literal string, never a glob.
#
#    This is v0.8.0's first CRITICAL: the attest action glob-expands
#    `subject-path` but fs.stat()s `sbom-path` VERBATIM, so `dist/furrow_*_...json`
#    stat'd a filename containing a literal `*` and threw "SBOM file not found"
#    — after the cask was already pushed. Asserting the exact pair per platform is
#    what would have caught it on the PR.
# ---------------------------------------------------------------------------
archives=""
for platform in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
  archive="furrow_${v}_${platform}.tar.gz"
  sbom="${archive}.spdx.sbom.json"
  archives="$archives $archive"

  [ -f "$dist/$archive" ] || note "attest subject-path does not exist: $dist/$archive"
  [ -f "$dist/$sbom" ]    || note "attest sbom-path does not exist: $dist/$sbom (sbom-path is NOT glob-expanded — it must be a concrete path)"

  # The SPDX version is load-bearing, not cosmetic: actions/attest derives the
  # attestation's predicateType FROM the document ("SPDX-2.3" ->
  # https://spdx.dev/Document/v2.3), and that predicate type is what the READMEs
  # tell users to pass to `gh attestation verify`. If syft's output version
  # floats, the docs go quietly wrong.
  if [ -f "$dist/$sbom" ] && ! grep -q '"spdxVersion"[: ]*"SPDX-2.3"' "$dist/$sbom"; then
    note "$sbom is not SPDX-2.3 — attest would publish a different predicateType than the READMEs document"
  fi
done

# ---------------------------------------------------------------------------
# 2. checksums.txt must name each archive on EXACTLY ONE line, as a whole field.
#
#    This is v0.8.0's second CRITICAL: an SBOM's name EXTENDS its archive's name
#    (furrow_X_linux_amd64.tar.gz ⊂ furrow_X_linux_amd64.tar.gz.spdx.sbom.json),
#    so once GoReleaser started listing SBOMs in checksums.txt, the consumer's
#    substring `grep -F "  $file"` matched TWO lines and fed sha256sum a checksum
#    for a file it never downloaded — breaking `furrow` install in every repo
#    pinning the release. The consumer now matches on $2; assert the shape that
#    makes that correct.
# ---------------------------------------------------------------------------
sums="$dist/checksums.txt"
if [ ! -f "$sums" ]; then
  note "no $sums"
else
  for archive in $archives; do
    n="$(awk -v f="$archive" '$2 == f' "$sums" | wc -l | tr -d ' ')"
    [ "$n" = "1" ] || note "checksums.txt names $archive on $n line(s) as an exact field — want exactly 1"
  done
fi

if [ "$fail" -ne 0 ]; then
  echo >&2
  echo "The release config produced artifacts that release.yml's next step cannot use." >&2
  echo "At a real tag this fails AFTER GoReleaser has published the draft and pushed" >&2
  echo "the Homebrew cask — i.e. it ships broken. Fix it here." >&2
  exit 1
fi

# release.yml consumes this instead of re-deriving the version itself, so the
# paths asserted above are literally the paths it attests.
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  echo "version=$v" >> "$GITHUB_OUTPUT"
fi

echo "ok — 4 archives + 4 SPDX-2.3 SBOMs, every attest path concrete, checksums.txt exact-field unique"
