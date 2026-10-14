#!/bin/sh
# check-docs-budget.sh — a word cap per prose doc. The docs regrew twice
# (54.8k words -> 26.4k when e-anwr reduced them, back to 32.4k by fee8188):
# each feature PR wrote its contract into the command's --help AND a README
# paragraph. A cap turns that growth into a decision — move the detail into
# the command's --help and point at it, or raise the cap HERE in the same
# change and say why in the commit. Pure text, no binary needed; called by
# scripts/check.sh and by CI (build.yml).
set -eu
cd "$(dirname "$0")/.."

status=0
total=0
while read -r file cap; do
  words="$(wc -w <"$file" | tr -d ' ')"
  total=$((total + words))
  if [ "$words" -gt "$cap" ]; then
    echo "✖ $file has $words words, over its budget of $cap: move command detail into that command's --help and point at it, or raise the cap in scripts/check-docs-budget.sh with a reason" >&2
    status=1
  fi
done <<'BUDGET'
README.md 9500
docs/architecture.md 4800
docs/glossary.md 3200
docs/non-goals.md 2700
docs/scheduling.md 1150
CLAUDE.md 1800
BUDGET

if [ "$total" -gt 24000 ]; then
  echo "✖ the docs total $total words, over the 24000 budget" >&2
  status=1
fi
if [ "$status" -eq 0 ]; then
  echo "ok — docs within their word budgets ($total of 24000)"
fi
exit "$status"
