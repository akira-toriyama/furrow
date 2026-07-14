# a closed task — closed and reviewed both stamped

`closed` and `reviewed` are pointers, so an open task serializes them as an
explicit `null`. This one has both set, so the fixture covers each state.
