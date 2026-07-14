# a frozen task carrying every field furrow persists

This board is a FIXTURE: its bytes are the assertion. See
`frozen_board_test.go` — Load → Save must reproduce every file exactly, so a
change to the shard's shape (or to the marshaller's recipe) cannot land without
either a deliberate schema bump or a visible rewrite of this committed board.

- [x] freeze a real board
- [ ] pin its bytes
