package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
)

// boardAxes is the pair `furrow board` now reports beside `source`.
type boardAxes struct {
	Source string `json:"source"`
	Mode   string `json:"mode"`
	Layout string `json:"layout"`
}

func readBoardAxes(t *testing.T) boardAxes {
	t.Helper()
	out, code := run(t, "board", "--json")
	if code != 0 {
		t.Fatalf("board --json exit = %d:\n%s", code, out)
	}
	var b boardAxes
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("parse board --json: %v\n%s", err, out)
	}
	return b
}

func writeBoardConfig(t *testing.T, body string) {
	t.Helper()
	cfg := filepath.Join(os.Getenv(app.EnvDir), "config.toml")
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// `furrow board` reports the two axes separately because neither can be derived
// from the other: MODE comes from the board's committed config.toml, LAYOUT from
// how discovery reached the store. A board declaring nothing is shared, and a
// store reached through FURROW_DIR is central (configuration, not a checkout).
func TestBoardReportsBothAxes(t *testing.T) {
	initStore(t)

	b := readBoardAxes(t)
	if b.Mode != "shared" {
		t.Errorf("a board declaring no mode must report shared, got %q", b.Mode)
	}
	if b.Layout != "central" {
		t.Errorf("FURROW_DIR is configuration, so layout must be central, got %q (source %q)", b.Layout, b.Source)
	}

	writeBoardConfig(t, "mode = \"standalone\"\n")
	if b := readBoardAxes(t); b.Mode != "standalone" {
		t.Errorf("mode = \"standalone\" must reach board --json, got %q", b.Mode)
	}

	// Clamp-don't-reject reaches the introspection view too: an unrecognized
	// mode reads back as the default rather than as itself.
	writeBoardConfig(t, "mode = \"hosted\"\n")
	if b := readBoardAxes(t); b.Mode != "shared" {
		t.Errorf("an unknown mode must clamp to shared in board --json, got %q", b.Mode)
	}

	// The retired bool is an unknown key now: carried on disk, never honoured.
	writeBoardConfig(t, "standalone = true\n")
	if b := readBoardAxes(t); b.Mode != "shared" {
		t.Errorf("the retired `standalone` key must not set the mode, got %q", b.Mode)
	}
}

// The human view carries the same pair, so an operator who never passes --json
// still sees which of the four shapes they are on.
func TestBoardHumanShowsBothAxes(t *testing.T) {
	initStore(t)
	out, code := run(t, "board")
	if code != 0 {
		t.Fatalf("board exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "mode:     shared") {
		t.Errorf("human board output must name the mode:\n%s", out)
	}
	if !strings.Contains(out, "layout:   central") {
		t.Errorf("human board output must name the layout:\n%s", out)
	}
}
