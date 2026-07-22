#!/bin/sh
# check-docs-commands.sh — assert docs/architecture.md's "### CLI commands" list
# names every top-level command the binary registers.
#
# The READMEs' command table is GENERATED from the cobra tree and drift-guarded
# (gen-command-table.sh + the README command-table guard). docs/architecture.md
# keeps its own hand-written command list, which had no guard and silently lost
# commands (brief/boards/doctor/ref were missing — the v5 docs-tier drift
# CLAUDE.md warns about: "drift pools exactly where no guard looks"). This closes
# that gap with pure text-extraction over `furrow commands` — the SAME source of
# truth gen-command-table.sh uses — so it needs no board and no toolchain beyond
# the build the rest of check.sh already does.
set -eu
cd "$(dirname "$0")/.."

bin="${FURROW_BIN:-}"
if [ -z "$bin" ]; then
	GOTOOLCHAIN=local go build -o bin/furrow ./cmd/furrow
	bin="$(pwd)/bin/furrow"
fi

# Top-level command names from the cobra tree. `furrow commands` prints one row
# per leaf as "| `<path><args>` | … | … |"; the first path word is the top-level
# command (`config init` -> config, `add <title>...` -> add). Hidden commands
# (including `commands` itself) and help/completion are already excluded there.
cmds="$("$bin" commands | sed -n 's/^| `\([a-z][a-z-]*\).*/\1/p' | sort -u)"

# Command tokens named in architecture.md's "### CLI commands" section: every
# backtick-wrapped lowercase token from that heading to the next heading/bullet.
listed="$(awk '/^### CLI commands/{f=1;next} f && /^(#|- )/{exit} f' docs/architecture.md \
	| grep -oE '`[a-z][a-z-]+`' | tr -d '`' | sort -u)"

missing=""
for c in $cmds; do
	printf '%s\n' "$listed" | grep -qx "$c" || missing="$missing $c"
done

if [ -n "$missing" ]; then
	printf 'check-docs-commands: docs/architecture.md "### CLI commands" omits:%s\n' "$missing" >&2
	printf '  the cobra tree is the source of truth — add the command(s) to that list.\n' >&2
	exit 1
fi
printf 'ok — docs/architecture.md lists all %s top-level commands\n' "$(printf '%s\n' "$cmds" | grep -c .)"
