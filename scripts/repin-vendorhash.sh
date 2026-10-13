#!/bin/sh
# Re-pin flake.nix's vendorHash to the go.sum in the tree, in one command.
#
# The ritual it performs is the one flake.nix's header describes (set
# vendorHash = pkgs.lib.fakeHash, `nix build`, paste the `got:` hash, refresh the
# `# go.sum sha256:` stamp) and the one scripts/check-version-lockstep.sh
# demands when the stamp disagrees with go.sum. Every dependabot gomod bump
# needs it — a fixed-output hash is nothing dependabot can write (#140, #396) —
# so the ritual has to be a single step a machine can run on a bump branch.
#
# Contract:
#   - exit 0 and no write when the stamp already matches go.sum (idempotent;
#     `--force` re-derives the hash anyway);
#   - exit 1 without touching flake.nix when nix is absent or `nix build` fails
#     for any reason other than the expected fixed-output mismatch;
#   - on success flake.nix carries the derived hash and the fresh stamp, and the
#     final `nix build` with that hash has passed — the file is never left on
#     fakeHash.
# Writes only flake.nix. Needs `nix` with flakes (the default since Nix 2.4 on
# the Determinate installer, and what the release/CI runners install).
set -eu
cd "$(dirname "$0")/.."

force=false
case "${1:-}" in
  "") ;;
  --force) force=true ;;
  *) echo "usage: scripts/repin-vendorhash.sh [--force]" >&2; exit 2 ;;
esac

if command -v shasum >/dev/null 2>&1; then
  gosum="$(shasum -a 256 go.sum | cut -d' ' -f1)"
else
  gosum="$(sha256sum go.sum | cut -d' ' -f1)"
fi
stamp="$(sed -n 's/^[[:space:]]*# go\.sum sha256: \([0-9a-f]\{64\}\).*/\1/p' flake.nix | head -1)"
current="$(sed -n 's/^[[:space:]]*vendorHash = "\(sha256-[A-Za-z0-9+\/=]*\)";.*/\1/p' flake.nix | head -1)"

if [ "$stamp" = "$gosum" ] && [ -n "$current" ] && [ "$force" = false ]; then
  echo "ok — flake.nix vendorHash stamp already matches go.sum ($current)"
  exit 0
fi

if ! command -v nix >/dev/null 2>&1; then
  echo "✖ nix is not on PATH — the vendorHash can only be derived by nix" >&2
  exit 1
fi

# Exactly one vendorHash line, whatever it currently holds (a pinned SRI hash,
# or a fakeHash left behind by an interrupted hand ritual).
n="$(grep -c '^[[:space:]]*vendorHash = ' flake.nix || true)"
if [ "$n" != 1 ]; then
  echo "✖ flake.nix must carry exactly one 'vendorHash = ' line (found $n)" >&2
  exit 1
fi

orig="$(mktemp)"
cp flake.nix "$orig"
restore() { cp "$orig" flake.nix; rm -f "$orig"; }

sed 's/^\([[:space:]]*\)vendorHash = .*;/\1vendorHash = pkgs.lib.fakeHash;/' "$orig" > flake.nix

# The probe build is EXPECTED to fail: a fixed-output derivation built against
# fakeHash reports the real hash as `got:`. Any other failure (eval error, no
# network, a go.mod the vendoring cannot satisfy) carries no `got:` and is the
# caller's to read.
out="$(nix build .# --no-link 2>&1 || true)"
got="$(printf '%s\n' "$out" | sed -n 's/.*got:[[:space:]]*\(sha256-[A-Za-z0-9+\/=]*\).*/\1/p' | head -1)"
if [ -z "$got" ]; then
  restore
  printf '%s\n' "$out" >&2
  echo "✖ nix build printed no 'got: sha256-…' — flake.nix left untouched" >&2
  exit 1
fi

sed -e "s|^\([[:space:]]*\)vendorHash = .*;|\1vendorHash = \"$got\";|" \
    -e "s/^\([[:space:]]*# go\.sum sha256: \)[0-9a-f]\{64\}/\1$gosum/" "$orig" > flake.nix

if ! grep -q "^[[:space:]]*# go\.sum sha256: $gosum" flake.nix; then
  restore
  echo "✖ flake.nix carries no '# go.sum sha256: <hash>' stamp line to refresh" >&2
  exit 1
fi

# The hash is only a pin once a build with it succeeds.
if ! nix build .# --no-link; then
  restore
  echo "✖ nix build failed with the derived vendorHash $got — flake.nix left untouched" >&2
  exit 1
fi
rm -f "$orig"

if [ "$got" = "$current" ]; then
  echo "re-pinned — vendorHash unchanged ($got); stamp refreshed to go.sum $gosum"
else
  echo "re-pinned — vendorHash $current -> $got; stamp refreshed to go.sum $gosum"
fi
