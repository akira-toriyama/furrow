#!/bin/sh
# gen-command-table.sh — regenerate the README's command table from the binary.
# The cobra tree (Use / Short / aliases / flags) is the single source of truth;
# this splices `furrow commands` output between the `<!-- commands:begin -->` /
# `<!-- commands:end -->` markers in README.md (command names, flags, and
# one-line descriptions are API surface, not prose). check.sh and CI diff the
# spliced block against a fresh run, so: edit Short/flags in internal/cli, run
# this, commit the result.
set -eu
cd "$(dirname "$0")/.."

GOTOOLCHAIN=local go build -o bin/furrow ./cmd/furrow

# BSD awk rejects a newline inside -v values, so hand the table over as a file.
table="$(mktemp)"
trap 'rm -f "$table"' EXIT
./bin/furrow commands > "$table"

awk -v tf="$table" '
  /<!-- commands:begin/ {
    print
    while ((getline line < tf) > 0) print line
    close(tf)
    skip = 1
    next
  }
  /<!-- commands:end/ { skip = 0 }
  !skip { print }
' README.md > README.md.gen-tmp
mv README.md.gen-tmp README.md
echo "ok — command table regenerated in README.md"
