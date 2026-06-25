package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var lanes = []string{"inbox", "backlog", "ready", "in-progress", "done", "icebox"}

func TestMapHeading(t *testing.T) {
	cases := map[string]string{
		"🎯 Open（優先度順）":          "ready",
		"🔬 要設計 / 要 triage":      "backlog",
		"🧊 温存（実害が出たら着手・LOWEST）": "icebox",
		"✅ Done（アーカイブ）":         "done",
		"📥 Inbox":               "inbox",
		"🔨 In Progress":         "in-progress",
		"Backlog":               "backlog",
	}
	known := map[string]bool{}
	for _, l := range lanes {
		known[l] = true
	}
	for h, want := range cases {
		got, ok := mapHeading(h, known)
		if !ok || got != want {
			t.Errorf("mapHeading(%q) = (%q,%v), want %q", h, got, ok, want)
		}
	}
}

// A compact synthetic Task.md exercising every shape: a heading-style Open
// section (### tasks with bold detail bullets that must NOT become tasks), a
// list-style triage section (bold-bullet tasks), and a Done <details> archive.
const synthetic = "# Task — demo tracker\n" +
	"\n> process preamble, not a task\n\n" +
	"## 🎯 Open（優先度順）\n" +
	"> 優先基準のメモ（無視される前置き）\n" +
	"### 1. first open task（旧 R1）\n" +
	"core の [Foo.swift:10](Sources/Foo.swift#L10) を直す。詳細は [[some-note]]。\n" +
	"- **詳細メモ** これはタスクではなく item 1 の本文。\n" +
	"- ふつうの sub-bullet。\n" +
	"### 2. second open task\n" +
	"https://example.com/spec を参照。\n" +
	"\n## 🔬 要設計 / triage\n" +
	"- **R7. キーボードが怪しい** — 症状未定義。[Bar.swift:5](Sources/Bar.swift#L5)。\n" +
	"- **C2. tree 編集** — pivot 中核。\n" +
	"\n## ✅ Done（アーカイブ）\n" +
	"<details><summary>完了</summary>\n" +
	"- **keyboard reorder**（#334）— 退行修正。\n" +
	"- **macOS min 14**（#333）— gate 撤去。\n" +
	"</details>\n" +
	"\n## 📎 付録 A: 設計詳細（✅完了→Done）\n" +
	"### A. これはタスクではない（付録は body 行き）\n" +
	"詳細な設計メモ。\n"

func TestParseSynthetic(t *testing.T) {
	res := Parse(synthetic, lanes, "inbox", 100, 10)

	got := map[string]Task{}
	for _, tk := range res.Tasks {
		got[tk.Title] = tk
	}

	// 6 tasks total: 2 open + 2 triage + 2 done. The bold DETAIL bullet under
	// open task 1 must NOT be a task.
	if len(res.Tasks) != 6 {
		t.Fatalf("expected 6 tasks, got %d: %v titles=%v", len(res.Tasks), res.Tasks, titles(res.Tasks))
	}
	if _, isTask := got["詳細メモ"]; isTask {
		t.Errorf("a bold detail bullet under a ### task was wrongly parsed as a task")
	}
	// appendix content must be skipped, not turned into a task.
	if _, isTask := got["これはタスクではない（付録は body 行き）"]; isTask {
		t.Errorf("an appendix (## 📎 付録) heading was wrongly parsed as a task")
	}
	if !anyContains(res.Warnings, "appendix") {
		t.Errorf("expected an appendix-skipped warning, got %v", res.Warnings)
	}

	// lanes
	checkLane(t, got, "first open task（旧 R1）", "ready")
	checkLane(t, got, "second open task", "ready")
	checkLane(t, got, "R7. キーボードが怪しい", "backlog")
	checkLane(t, got, "keyboard reorder", "done")

	// per-lane sparse priority preserves document order.
	if got["first open task（旧 R1）"].Priority != 100 || got["second open task"].Priority != 110 {
		t.Errorf("open lane priorities wrong: %d, %d",
			got["first open task（旧 R1）"].Priority, got["second open task"].Priority)
	}

	// refs: markdown link target + bare URL captured; wikilink left in body.
	first := got["first open task（旧 R1）"]
	if !hasRef(first.Refs, "Sources/Foo.swift#L10") {
		t.Errorf("expected file:line ref, got %v", first.Refs)
	}
	if !strings.Contains(first.Body, "[[some-note]]") {
		t.Errorf("wikilink should remain in body, body=%q", first.Body)
	}
	if !hasRef(got["second open task"].Refs, "https://example.com/spec") {
		t.Errorf("expected bare URL ref, got %v", got["second open task"].Refs)
	}

	// the bold detail bullet's text is in the task body.
	if !strings.Contains(first.Body, "詳細メモ") {
		t.Errorf("detail bullet text should be in body, body=%q", first.Body)
	}

	// body starts with the title heading.
	if !strings.HasPrefix(first.Body, "# first open task（旧 R1）\n") {
		t.Errorf("body should start with the title heading, got %q", first.Body)
	}

	// the <details>/<summary> wrappers must not leak into a body.
	for _, tk := range res.Tasks {
		if strings.Contains(tk.Body, "<details>") || strings.Contains(tk.Body, "<summary>") {
			t.Errorf("HTML wrapper leaked into body of %q", tk.Title)
		}
	}

	// wikilink advisory present.
	if !anyContains(res.Warnings, "wikilink") {
		t.Errorf("expected a wikilink warning, got %v", res.Warnings)
	}
}

func TestRefExtractionCJKAndTitleAttr(t *testing.T) {
	md := "## 🎯 Open\n" +
		"### only task\n" +
		"参照（https://example.com/spec）して直す。\n" +
		"see [doc](https://example.com/x \"a title\") and [code](Sources/Foo.swift#L10)。\n"
	res := Parse(md, lanes, "inbox", 100, 10)
	if len(res.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(res.Tasks))
	}
	refs := res.Tasks[0].Refs
	// fullwidth paren + trailing Japanese must be trimmed off the URL.
	if !hasRef(refs, "https://example.com/spec") {
		t.Errorf("CJK-abutting URL not cleaned: %v", refs)
	}
	for _, r := range refs {
		if strings.ContainsAny(r, "（）。\" ") {
			t.Errorf("ref %q still carries CJK/title/space junk: %v", r, refs)
		}
	}
	// title attr collapsed so it does not double with the bare-URL pass.
	if !hasRef(refs, "https://example.com/x") {
		t.Errorf("title-attr URL not cleaned to bare url: %v", refs)
	}
	if !hasRef(refs, "Sources/Foo.swift#L10") {
		t.Errorf("file:line ref missing: %v", refs)
	}
}

func TestEmbeddedHeadingInListSectionKeepsTasks(t *testing.T) {
	// A list-style section that grows a stray ### must NOT swallow the bold
	// bullets that follow it.
	md := "## 🔬 triage\n" +
		"- **R1. first** — a.\n" +
		"### a sub-heading that is really detail\n" +
		"- **R2. second** — b.\n" +
		"- **R3. third** — c.\n"
	res := Parse(md, lanes, "inbox", 100, 10)
	titlesSeen := map[string]bool{}
	for _, tk := range res.Tasks {
		titlesSeen[tk.Title] = true
	}
	for _, want := range []string{"R1. first", "R2. second", "R3. third"} {
		if !titlesSeen[want] {
			t.Errorf("task %q was swallowed by an embedded ###; got %v", want, titles(res.Tasks))
		}
	}
	if len(res.Tasks) != 3 {
		t.Errorf("expected 3 tasks, got %d: %v", len(res.Tasks), titles(res.Tasks))
	}
}

// The real facet Task.md must parse without panicking and produce sane output.
func TestParseRealFacetTaskMd(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "facet-task.md"))
	if err != nil {
		t.Fatal(err)
	}
	res := Parse(string(b), lanes, "inbox", 100, 10)
	if len(res.Tasks) == 0 {
		t.Fatal("expected some tasks from the real Task.md")
	}
	// Every task must land in a real lane and have a non-empty title + body.
	known := map[string]bool{}
	for _, l := range lanes {
		known[l] = true
	}
	for _, tk := range res.Tasks {
		if !known[tk.Status] {
			t.Errorf("task %q has non-config lane %q", tk.Title, tk.Status)
		}
		if strings.TrimSpace(tk.Title) == "" {
			t.Errorf("task with empty title: %+v", tk)
		}
		if !strings.HasPrefix(tk.Body, "# ") {
			t.Errorf("task %q body missing heading: %q", tk.Title, tk.Body[:min(40, len(tk.Body))])
		}
	}
	// The Done archive items should be present and mapped to done.
	if !anyTaskInLane(res.Tasks, "done") {
		t.Errorf("expected at least one done task from the archive section")
	}
	t.Logf("parsed %d tasks, %d warnings from real facet Task.md", len(res.Tasks), len(res.Warnings))
}

// helpers
func titles(ts []Task) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Title)
	}
	return out
}
func checkLane(t *testing.T, got map[string]Task, title, lane string) {
	t.Helper()
	if got[title].Status != lane {
		t.Errorf("task %q lane = %q, want %q", title, got[title].Status, lane)
	}
}
func hasRef(refs []string, want string) bool {
	for _, r := range refs {
		if r == want {
			return true
		}
	}
	return false
}
func anyContains(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
func anyTaskInLane(ts []Task, lane string) bool {
	for _, t := range ts {
		if t.Status == lane {
			return true
		}
	}
	return false
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
