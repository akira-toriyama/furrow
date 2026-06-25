package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
	"golang.org/x/term"
)

// JSON goes to stdout ONLY; logs, spinners, and errors go to stderr (MEMO §4).
// These helpers are the single funnel for that rule.

// out is stdout; overridable in tests.
var out io.Writer = os.Stdout

// mustJSON marshals deterministically (SetEscapeHTML(false), 2-space indent) so
// CLI JSON output matches the index's encoding style.
func mustJSON(v any) []byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	_ = e.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}

// printJSON writes a value as indented JSON to stdout.
func printJSON(v any) {
	fmt.Fprintln(out, string(mustJSON(v)))
}

// printNDJSON writes one compact JSON object per line.
func printNDJSON(tasks []core.Task) {
	for _, t := range tasks {
		var b bytes.Buffer
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false)
		_ = e.Encode(t) // Encode adds the newline
		fmt.Fprint(out, b.String())
	}
}

// isTTY reports whether stdout is a terminal — used to pick table vs plain
// output and to gate destructive ops in non-interactive contexts.
func isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// printTaskTable renders tasks as an aligned text table (human output). It is
// deliberately plain (no box drawing) so it greps and copies cleanly.
func printTaskTable(tasks []core.Task) {
	if len(tasks) == 0 {
		fmt.Fprintln(out, "(no tasks)")
		return
	}
	// column widths
	wID, wStatus := len("ID"), len("STATUS")
	for _, t := range tasks {
		if len(t.ID) > wID {
			wID = len(t.ID)
		}
		if len(t.Status) > wStatus {
			wStatus = len(t.Status)
		}
	}
	fmt.Fprintf(out, "%-*s  %-*s  %5s  %s\n", wID, "ID", wStatus, "STATUS", "PRIO", "TITLE")
	for _, t := range tasks {
		title := t.Title
		if len(t.Labels) > 0 {
			title += "  [" + strings.Join(t.Labels, ",") + "]"
		}
		fmt.Fprintf(out, "%-*s  %-*s  %5d  %s\n", wID, t.ID, wStatus, t.Status, t.Priority, title)
	}
}

// emitTasks renders a task list per the active output mode (--json | --ndjson |
// human table). emptyIsNotFound makes an empty result exit 1 (the "empty" arm
// of the contract) — used by commands where "nothing matched" is a soft miss.
func emitTasks(tasks []core.Task, emptyIsNotFound bool) error {
	switch {
	case flagNDJSON:
		printNDJSON(tasks)
	case flagJSON:
		if tasks == nil {
			tasks = []core.Task{}
		}
		printJSON(tasks)
	default:
		printTaskTable(tasks)
	}
	if emptyIsNotFound && len(tasks) == 0 {
		return &core.Error{Code: core.CodeNotFound, Msg: "no matching tasks"}
	}
	return nil
}

// taskView is the JSON shape for `show`: the task plus its resolved body text.
type taskView struct {
	core.Task
	BodyText string `json:"body_text"`
}

// printTaskDetail renders a single task for `show` / after a mutation.
func printTaskDetail(t *core.Task, body string) {
	if flagJSON {
		printJSON(taskView{Task: *t, BodyText: body})
		return
	}
	fmt.Fprintf(out, "%s  %s\n", t.ID, t.Title)
	fmt.Fprintf(out, "status:   %s\n", t.Status)
	fmt.Fprintf(out, "priority: %d\n", t.Priority)
	if len(t.Labels) > 0 {
		fmt.Fprintf(out, "labels:   %s\n", strings.Join(t.Labels, ", "))
	}
	if t.Parent != "" {
		fmt.Fprintf(out, "parent:   %s\n", t.Parent)
	}
	if len(t.Deps) > 0 {
		fmt.Fprintf(out, "deps:     %s\n", strings.Join(t.Deps, ", "))
	}
	if len(t.Refs) > 0 {
		fmt.Fprintf(out, "refs:     %s\n", strings.Join(t.Refs, ", "))
	}
	for _, c := range t.Checklist {
		box := "[ ]"
		if c.Done {
			box = "[x]"
		}
		fmt.Fprintf(out, "  %s %s\n", box, c.Text)
	}
	fmt.Fprintf(out, "created:  %s\n", t.Created.Format("2006-01-02 15:04"))
	fmt.Fprintf(out, "updated:  %s\n", t.Updated.Format("2006-01-02 15:04"))
	if t.Closed != nil {
		fmt.Fprintf(out, "closed:   %s\n", t.Closed.Format("2006-01-02 15:04"))
	}
	if strings.TrimSpace(body) != "" {
		fmt.Fprintf(out, "\n%s\n", strings.TrimRight(body, "\n"))
	}
}

// printOK prints a short confirmation line for a mutation (human mode) or the
// task JSON (--json mode).
func printOK(verb string, t *core.Task) {
	if flagJSON {
		printJSON(t)
		return
	}
	fmt.Fprintf(out, "%s %s  %s\n", verb, t.ID, t.Title)
}

// atoiArg parses a CLI integer argument into a validation error on failure.
func atoiArg(name, s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, core.Validationf("", "%s must be an integer, got %q", name, s)
	}
	return n, nil
}
