package main

import (
	"fmt"
	"testing"
)

// scaledTasks inflates the 24-task fixture to n tasks by cloning it with fresh
// ids, so the board has a realistic per-lane depth. The real central board is
// 658 tasks; the POC fixture is 24, so nothing here had measured what a full
// board costs to render.
func scaledTasks(n int) []*Task {
	base := fixtureTasks()
	out := make([]*Task, 0, n)
	for i := 0; len(out) < n; i++ {
		for _, t := range base {
			if len(out) >= n {
				break
			}
			c := *t
			if i > 0 {
				c.ID = fmt.Sprintf("%s-%d", t.ID, i)
				c.Parent = "" // clones must not re-point at the original epic
				c.Deps = nil  // nor duplicate its dep edges
				c.Priority = t.Priority + i*1000
			}
			out = append(out, &c)
		}
	}
	return out
}

func scaledModel(n, w, h int) *Model {
	m := New(&mockProvider{b: NewBoard(scaledTasks(n))})
	m.w, m.h = w, h
	m.recompute()
	m.relayout()
	return m
}

// BenchmarkViewAtBoardSize measures one full frame render. A TUI has to repaint
// on every keystroke and on every mouse-motion event during a drag, so the
// budget is a frame time comfortably under ~16ms; anything approaching that
// makes dragging feel heavy.
func BenchmarkViewAtBoardSize(b *testing.B) {
	for _, n := range []int{24, 100, 658, 2000} {
		b.Run(fmt.Sprintf("tasks=%d", n), func(b *testing.B) {
			m := scaledModel(n, 150, 40)
			b.ReportAllocs()
			for b.Loop() {
				_ = m.View()
			}
		})
	}
}

// BenchmarkRecomputeAtBoardSize measures the filter+group pass that runs
// whenever the board or the query changes (not on every keystroke).
func BenchmarkRecomputeAtBoardSize(b *testing.B) {
	for _, n := range []int{24, 658, 2000} {
		b.Run(fmt.Sprintf("tasks=%d", n), func(b *testing.B) {
			m := scaledModel(n, 150, 40)
			b.ReportAllocs()
			for b.Loop() {
				m.recompute()
			}
		})
	}
}

// TestScalesToARealBoard is the assertion, not just a measurement: a full-size
// board must still render a sane frame.
func TestScalesToARealBoard(t *testing.T) {
	m := scaledModel(658, 150, 40)
	out := ansiStrip(m.View().Content)
	if len(out) == 0 {
		t.Fatal("658-task board rendered an empty frame")
	}
	if n := len(m.b.Tasks()); n != 658 {
		t.Fatalf("expected 658 tasks, got %d", n)
	}
}
