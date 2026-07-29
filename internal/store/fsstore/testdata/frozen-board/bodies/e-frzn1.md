# a frozen epic carrying every field furrow persists

Its body lives in the SHARED bodies/ directory, beside the tasks' — that sharing
is a design decision this fixture pins, not an accident: ids are prefix-disjoint,
so `furrow edit`, `sync -b`, the union-merge rule and the orphan-body lint all
work on an epic without a second code path.
