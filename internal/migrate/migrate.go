// Package migrate parses a hand-maintained "Task.md"-style markdown tracker
// (the thing furrow replaces) into furrow tasks. The canonical pain it untangles
// is one file holding: an Open priority list, a needs-design/triage list, a
// parked list, a Done archive (often in <details>), plus design appendices and
// process prose — see MEMO §11 for the facet Task.md study this targets.
//
// Parse is pure (string in, structured result out) so it is fully testable
// against a real fixture; the CLI wires it to the store. It is intentionally
// conservative and LOUD: anything it cannot map is reported as a warning rather
// than silently dropped (未達成を暗黙にしない). `furrow migrate` defaults to a
// dry-run preview so a human reviews the plan before anything is written.
package migrate

import (
	"fmt"
	"regexp"
	"strings"
)

// Task is one parsed item, ready to feed to app.Add.
type Task struct {
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Priority int      `json:"priority"`
	Body     string   `json:"body"`
	Refs     []string `json:"refs"`
}

// Result is the outcome of a parse: the tasks plus warnings (unmapped headings,
// tasks found before any section, unresolved [[wikilinks]], etc.).
type Result struct {
	Tasks    []Task   `json:"tasks"`
	Warnings []string `json:"warnings"`
}

var (
	reHeading2  = regexp.MustCompile(`^##\s+(.+?)\s*$`)                 // "## 🎯 Open"
	reHeading3  = regexp.MustCompile(`^#{3,6}\s+(.+?)\s*$`)             // "### 1. Title"
	reLeadNum   = regexp.MustCompile(`^\d+\.\s+`)                       // "1. " prefix on a heading
	reBoldItem  = regexp.MustCompile(`^-\s+\*\*(.+?)\*\*\s*(.*)$`)      // "- **Title** rest" (top-level)
	reMDLink    = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)           // [text](target)
	reBareURL   = regexp.MustCompile(`https?://[^\s)>\]]+`)             // bare http(s) URL
	reWikiLink  = regexp.MustCompile(`\[\[([^\]]+)\]\]`)                // [[wikilink]]
	reHTMLAside = regexp.MustCompile(`^</?(details|summary)[^>]*>\s*$`) // <details>/<summary> wrappers
)

// Parse converts Task.md markdown into furrow tasks. laneOrder/defaultLane come
// from config so the mapped lanes are validated against the user's actual lanes;
// priorityBase/priorityStep set the sparse per-lane priorities (document order
// is preserved). Section semantics:
//   - "## <heading>"            sets the current lane (heuristic mapHeading).
//   - "### [N.] Title"          starts a task (heading-style section).
//   - "- **Title** ..."         starts a task ONLY in a section that has no
//     "###" tasks (list-style: triage / parked / the
//     Done <details> archive). Otherwise it is detail.
//   - everything else           is appended to the current task's body.
func Parse(md string, laneOrder []string, defaultLane string, priorityBase, priorityStep int) Result {
	lanes := map[string]bool{}
	for _, l := range laneOrder {
		lanes[l] = true
	}

	var (
		res        Result
		curLane    string
		sawSection bool
		// sectionStyle: "" unknown, "heading" (### tasks), "list" (bold-bullet tasks)
		sectionStyle string
		// skipSection is set for appendix sections (## 📎 付録 …): their content
		// is design detail that belongs in a task's body, not its own tasks, and
		// we can't reliably match it to a parent — so we skip + warn rather than
		// invent bogus tasks (未達成を暗黙にしない).
		skipSection bool
		cur         *parsing // the task currently accumulating body lines
		bodyLines   []string
		counters    = map[string]int{} // per-lane sparse priority cursor
	)

	flush := func() {
		if cur == nil {
			return
		}
		t := finalize(*cur, bodyLines)
		res.Tasks = append(res.Tasks, t)
		cur = nil
		bodyLines = nil
	}

	nextPriority := func(lane string) int {
		if _, ok := counters[lane]; !ok {
			counters[lane] = priorityBase
		} else {
			counters[lane] += priorityStep
		}
		return counters[lane]
	}

	start := func(title, lane, trailing string) {
		flush()
		cur = &parsing{title: strings.TrimSpace(title), lane: lane, priority: nextPriority(lane)}
		bodyLines = nil
		if s := strings.TrimSpace(trailing); s != "" {
			bodyLines = append(bodyLines, s)
		}
	}

	for _, line := range strings.Split(md, "\n") {
		// Section heading: "## ..."
		if m := reHeading2.FindStringSubmatch(line); m != nil {
			flush()
			sectionStyle = ""
			sawSection = true
			// Appendix sections (## 📎 付録 …) are skipped with a warning.
			if isAppendix(m[1]) {
				skipSection = true
				curLane = ""
				res.Warnings = append(res.Warnings, fmt.Sprintf("appendix section %q skipped — fold it into the related task's body by hand", m[1]))
				continue
			}
			skipSection = false
			lane, ok := mapHeading(m[1], lanes)
			if !ok {
				if mapHeadingRaw(m[1]) == "" {
					res.Warnings = append(res.Warnings, fmt.Sprintf("unmapped section heading %q -> using default lane %q", m[1], defaultLane))
				} else {
					res.Warnings = append(res.Warnings, fmt.Sprintf("section %q maps to a lane not in config -> using default lane %q", m[1], defaultLane))
				}
				lane = defaultLane
			}
			curLane = lane
			continue
		}

		// Inside an appendix section: ignore everything until the next "##".
		if skipSection {
			continue
		}

		// Task heading: "### [N.] Title"
		if m := reHeading3.FindStringSubmatch(line); m != nil {
			lane := curLane
			if !sawSection {
				lane = defaultLane
				res.Warnings = append(res.Warnings, "task before any section heading -> default lane "+defaultLane)
			}
			sectionStyle = "heading"
			title := reLeadNum.ReplaceAllString(m[1], "")
			start(title, lane, "")
			continue
		}

		// Top-level bold bullet: a task only in a list-style section.
		if m := reBoldItem.FindStringSubmatch(line); m != nil && sectionStyle != "heading" {
			lane := curLane
			if !sawSection {
				lane = defaultLane
				res.Warnings = append(res.Warnings, "task before any section heading -> default lane "+defaultLane)
			}
			sectionStyle = "list"
			start(m[1], lane, m[2])
			continue
		}

		// <details>/<summary> wrappers are noise — drop them.
		if reHTMLAside.MatchString(strings.TrimSpace(line)) {
			continue
		}

		// Any other line is body for the active task (preamble before the first
		// task in a section is ignored).
		if cur != nil {
			bodyLines = append(bodyLines, line)
		}
	}
	flush()

	// Count unresolved wikilinks across all tasks as a single advisory.
	if n := countWikilinks(res.Tasks); n > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("found %d [[wikilink]](s); left in body text, not resolved to frozen ids", n))
	}
	return res
}

// parsing is the in-progress accumulator for one task.
type parsing struct {
	title    string
	lane     string
	priority int
}

// finalize turns an accumulator + its body lines into a Task: it trims the body,
// builds a "# Title\n\n<body>" markdown, and extracts refs from the whole text.
func finalize(p parsing, bodyLines []string) Task {
	body := strings.TrimRight(strings.Join(bodyLines, "\n"), "\n \t")
	body = strings.TrimLeft(body, "\n")

	full := p.title + "\n" + body
	refs := extractRefs(full)

	md := "# " + p.title + "\n"
	if strings.TrimSpace(body) != "" {
		md += "\n" + body + "\n"
	}
	return Task{Title: p.title, Status: p.lane, Priority: p.priority, Body: md, Refs: refs}
}

// extractRefs pulls markdown-link targets and bare URLs (deduped, in order).
// Wikilinks are intentionally NOT turned into refs (their targets are old slugs
// with no furrow id yet); they stay in the body.
func extractRefs(text string) []string {
	seen := map[string]bool{}
	var refs []string
	add := func(r string) {
		r = strings.TrimSpace(r)
		if r == "" || seen[r] {
			return
		}
		seen[r] = true
		refs = append(refs, r)
	}
	for _, m := range reMDLink.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	for _, u := range reBareURL.FindAllString(text, -1) {
		add(u)
	}
	return refs
}

func countWikilinks(tasks []Task) int {
	n := 0
	for _, t := range tasks {
		n += len(reWikiLink.FindAllString(t.Body, -1))
	}
	return n
}

// isAppendix reports whether a "## " heading is a design appendix (## 📎 付録 …
// / "Appendix …"). These hold detail that belongs in a task body, not its own
// tasks, so the parser skips them with a warning.
func isAppendix(h string) bool {
	l := strings.ToLower(h)
	return strings.Contains(h, "付録") || strings.Contains(h, "📎") || strings.Contains(l, "appendix")
}

// mapHeading resolves a "## " heading to a configured lane. Returns (lane, true)
// only when the heuristic lane is actually one of laneOrder.
func mapHeading(h string, lanes map[string]bool) (string, bool) {
	lane := mapHeadingRaw(h)
	if lane == "" {
		return "", false
	}
	if lanes[lane] {
		return lane, true
	}
	return "", false
}

// mapHeadingRaw is the keyword/emoji heuristic (config-independent). Keyword text
// wins over emoji (facet uses "✅ Done" where Projects uses "✅ Ready", so the
// word "Done" is the reliable signal). Returns "" when nothing matches.
func mapHeadingRaw(h string) string {
	l := strings.ToLower(h)
	switch {
	case strings.Contains(l, "done"):
		return "done"
	case strings.Contains(l, "icebox") || strings.Contains(h, "温存") || strings.Contains(h, "🧊"):
		return "icebox"
	case strings.Contains(l, "in progress") || strings.Contains(l, "in-progress") || strings.Contains(h, "🔨"):
		return "in-progress"
	case strings.Contains(l, "ready"):
		return "ready"
	case strings.Contains(l, "backlog") || strings.Contains(h, "要設計") || strings.Contains(l, "triage") || strings.Contains(h, "🔬") || strings.Contains(h, "📋"):
		return "backlog"
	case strings.Contains(l, "inbox") || strings.Contains(h, "📥"):
		return "inbox"
	case strings.Contains(l, "open") || strings.Contains(h, "🎯"):
		return "ready"
	case strings.Contains(h, "✔"):
		return "done"
	case strings.Contains(h, "✅"):
		return "ready"
	}
	return ""
}
