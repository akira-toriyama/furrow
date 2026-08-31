package app

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// syncTrailer is the attribution line a sync auto-commit appends below its
// subject: `Furrow-sync: host=<h> pid=<n> via=<comm<comm<…> dir=<board>`. It
// exists because a shared board's sync commits are otherwise indistinguishable
// — author and committer are the same configured identity on every machine, so
// when three identical "sync via furrow" commits appeared that nobody
// remembered running, git could not say which machine, checkout, or launcher
// made them, and a wrong guess ("another operator on this checkout") was
// reported to the user as fact (t-zrjf). host answers the multi-machine
// question; pid separates concurrent executions on one host; via — the
// parent-process chain, child-most first — is what tells an agent-launched
// sync from a human terminal's, the distinction that incident actually needed.
// dir comes last because a path may contain spaces and every field before it
// stays machine-splittable. Each field is best-effort: one that cannot be read
// is omitted rather than failing or slowing the sync.
func syncTrailer(dir string) string {
	parts := make([]string, 0, 4)
	if h, err := os.Hostname(); err == nil && h != "" {
		parts = append(parts, "host="+h)
	}
	parts = append(parts, "pid="+strconv.Itoa(os.Getpid()))
	if chain := parentChain(4); len(chain) > 0 {
		parts = append(parts, "via="+strings.Join(chain, "<"))
	}
	if dir != "" {
		parts = append(parts, "dir="+dir)
	}
	return "Furrow-sync: " + strings.Join(parts, " ")
}

// parentChain walks the process ancestry starting at this process's parent,
// child-most first, capped at max hops — e.g. ["zsh", "node", "launchd"]. One
// comm level is not enough on its own: an agent's shell and a human's are both
// "zsh", and only the level above separates them. Linux reads /proc (file
// reads, no subprocess); elsewhere it asks ps(1) per hop. Any failure ends the
// walk with what was gathered — attribution is telemetry, never a reason a
// sync could fail or stall.
func parentChain(max int) []string {
	var chain []string
	pid := os.Getppid()
	for range max {
		if pid <= 1 {
			break
		}
		name, ppid, ok := procInfo(pid)
		if !ok {
			break
		}
		chain = append(chain, name)
		pid = ppid
	}
	return chain
}

// procInfo resolves one pid to its short command name and parent pid.
func procInfo(pid int) (name string, ppid int, ok bool) {
	if runtime.GOOS == "linux" {
		if name, ppid, ok = procInfoStat(pid); ok {
			return name, ppid, true
		}
		// /proc can be absent or masked in odd sandboxes — fall through to ps.
	}
	// #nosec G204 -- fixed binary name; the only variable is a pid rendered
	// from an int.
	out, err := exec.Command("ps", "-o", "ppid=,ucomm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", 0, false
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return "", 0, false
	}
	p, err := strconv.Atoi(fields[0])
	if err != nil {
		return "", 0, false
	}
	return strings.Join(fields[1:], " "), p, true
}

// procInfoStat parses /proc/<pid>/stat: `pid (comm) state ppid …`. comm may
// itself contain spaces or parens, so the name ends at the LAST ')'.
func procInfoStat(pid int) (name string, ppid int, ok bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", 0, false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	j := strings.IndexByte(s, '(')
	if i < 0 || j < 0 || j >= i {
		return "", 0, false
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 2 {
		return "", 0, false
	}
	p, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, false
	}
	return s[j+1 : i], p, true
}
