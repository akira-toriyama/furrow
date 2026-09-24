package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// batchLine is one NDJSON object of `add --batch`. Pointers and nil slices
// tell "absent" from "present and empty": an absent scalar falls back to the
// shared flag, an explicit `"epic": ""` means unfiled on purpose (add's
// `-e ”`), and a list field unions with the flag's values.
type batchLine struct {
	Key       string   `json:"key"`
	Title     string   `json:"title"`
	Status    *string  `json:"status"`
	Priority  *int     `json:"priority"`
	Value     *int     `json:"value"`
	Effort    *int     `json:"effort"`
	Labels    []string `json:"labels"`
	Repos     []string `json:"repos"`
	Draft     *bool    `json:"draft"`
	Epic      *string  `json:"epic"`
	Deps      []string `json:"deps"`
	Refs      []string `json:"refs"`
	Body      *string  `json:"body"`
	Checklist []string `json:"checklist"`
	Due       *string  `json:"due"`
	Repeat    *string  `json:"repeat"`
}

// batchFields is the closed field vocabulary — the JSON names of batchLine.
// An unknown field is exit 2 with this list in candidates (config set's
// rule: a typo must not silently create a task without the field).
var batchFields = []string{"key", "title", "status", "priority", "value", "effort", "labels", "repos", "draft", "epic", "deps", "refs", "body", "checklist", "due", "repeat"}

// keyedTaskView is a created task with the batch key it was declared under —
// the ONE place a key is visible after the write, since shards never carry
// it. core.Task is embedded (the usual rule: it must never grow a
// MarshalJSON, or key would silently vanish).
type keyedTaskView struct {
	core.Task
	Key string `json:"key,omitempty"`
}

// addFromBatch reads NDJSON from src ("-" = stdin, else a path), merges each
// line over the shared flags, and creates every task in one write through
// app.AddBatch. stdout is the created tasks (each with its key in JSON);
// the stderr notes are the same ones every add path prints.
func addFromBatch(cmd *cobra.Command, a *app.App, src string, shared app.AddOpts) error {
	r := cmd.InOrStdin()
	if src != "-" {
		data, err := os.ReadFile(src) // #nosec G304 -- the path is the caller's own --batch argument, read once and never written
		if err != nil {
			return core.Validationf("", "--batch: %v", err)
		}
		r = bytes.NewReader(data)
	}
	specs, err := parseBatch(r, shared)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return core.Validationf("", "--batch: no task objects in the input")
	}
	created, keys, err := a.AddBatch(specs)
	if err != nil {
		return err
	}
	drafted := len(created) > 0 && len(created[0].Repos) == 0
	warnShadowedDraft(a, shared.Draft, drafted)
	noteInheritedEpic(cmd, created)
	noteRepeatBinds(a, created)
	views := make([]keyedTaskView, 0, len(created))
	for i := range created {
		views = append(views, keyedTaskView{Task: created[i], Key: keys[i]})
	}
	emitList(views, func() { printTaskTable(a, created) })
	return nil
}

// parseBatch decodes NDJSON into batch specs. Each line is checked against the
// field vocabulary BEFORE it is decoded, so a misspelled field is named with
// its line number rather than silently ignored.
func parseBatch(r io.Reader, shared app.AddOpts) ([]app.BatchSpec, error) {
	var specs []app.BatchSpec
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // a body rides the line
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			return nil, core.Validationf("", "--batch line %d: not a JSON object: %v", lineNo, err)
		}
		var unknown []string
		for k := range fields {
			if !slices.Contains(batchFields, k) {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return nil, &core.Error{
				Code:       core.CodeValidation,
				Kind:       core.KindValidation,
				Msg:        fmt.Sprintf("--batch line %d: unknown field(s) %s (valid: %s)", lineNo, strings.Join(unknown, ", "), strings.Join(batchFields, ", ")),
				Candidates: append([]string(nil), batchFields...),
			}
		}
		var l batchLine
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			return nil, core.Validationf("", "--batch line %d: %v", lineNo, err)
		}
		if strings.TrimSpace(l.Title) == "" {
			return nil, core.Validationf("", "--batch line %d: title is required", lineNo)
		}
		specs = append(specs, app.BatchSpec{Key: l.Key, AddSpec: app.AddSpec{Title: l.Title, AddOpts: l.merge(shared)}})
	}
	if err := sc.Err(); err != nil {
		return nil, core.Internalf("", "--batch: reading input: %v", err)
	}
	return specs, nil
}

// merge lays the line's fields over the shared flags: a present scalar
// replaces, a list unions (flag values first), and `"epic": ""` is add's
// explicit unfiled.
func (l batchLine) merge(shared app.AddOpts) app.AddOpts {
	o := shared
	if l.Status != nil {
		o.Status = *l.Status
	}
	if l.Priority != nil {
		p := *l.Priority
		o.Priority = &p
	}
	if l.Value != nil {
		v := *l.Value
		o.Value = &v
	}
	if l.Effort != nil {
		e := *l.Effort
		o.Effort = &e
	}
	if l.Draft != nil {
		o.Draft = *l.Draft
	}
	if l.Epic != nil {
		o.Epic = *l.Epic
		o.NoEpic = *l.Epic == ""
	}
	if l.Body != nil {
		o.Body = *l.Body
	}
	if l.Due != nil {
		o.Due = *l.Due
	}
	if l.Repeat != nil {
		o.Repeat = *l.Repeat
	}
	o.Labels = unionStrings(shared.Labels, l.Labels)
	o.Repos = unionStrings(shared.Repos, l.Repos)
	o.Deps = unionStrings(shared.Deps, l.Deps)
	o.Refs = unionStrings(shared.Refs, l.Refs)
	o.Checklist = unionStrings(shared.Checklist, l.Checklist)
	return o
}

// unionStrings appends b's entries not already in a, order preserved; nil in,
// nil out, so an absent list stays "unset" for the app's blank-value checks.
func unionStrings(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := append([]string(nil), a...)
	for _, s := range b {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
