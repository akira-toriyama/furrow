#!/usr/bin/env bash
# run.sh — build furrow and run it, so an agent (or you) can iterate without
# rebuilding by hand. Mirrors the family dev-loop convention (chord/jig/perch):
# set -euo pipefail, cd to the script dir, → progress echoes.
#
#   ./run.sh                 build + run with no args (prints help)
#   ./run.sh ls --json       build + run a CLI subcommand (args passed through)
#   ./run.sh ui              build + launch the TUI
set -euo pipefail
cd "$(dirname "$0")"

echo "→ go build -o bin/furrow ./cmd/furrow"
GOTOOLCHAIN=local go build -o bin/furrow ./cmd/furrow

echo "→ ./bin/furrow $*"
exec ./bin/furrow "$@"
