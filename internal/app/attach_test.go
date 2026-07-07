package app

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

func TestAttachAppendsImageLineToBody(t *testing.T) {
	a := newSeededApp()
	tk, _ := a.Add("bug", AddOpts{Status: "ready"})

	res, err := a.Attach(tk.ID, "shot.png", []byte{0x89, 'P', 'N', 'G'})
	if err != nil {
		t.Fatal(err)
	}
	wantName := tk.ID + "-shot.png"
	if res.Name != wantName {
		t.Errorf("Name = %q, want %q", res.Name, wantName)
	}
	if res.Path != "bodies/assets/"+wantName {
		t.Errorf("Path = %q", res.Path)
	}
	if res.Ref != "assets/"+wantName {
		t.Errorf("Ref = %q", res.Ref)
	}
	wantLine := "![shot.png](assets/" + wantName + ")"
	if res.Line != wantLine {
		t.Errorf("Line = %q, want %q", res.Line, wantLine)
	}

	_, body, _ := a.Get(tk.ID)
	if !strings.Contains(body, wantLine) {
		t.Errorf("body missing attach line; body =\n%s", body)
	}
}

func TestAttachVideoUsesLinkNotImage(t *testing.T) {
	a := newSeededApp()
	tk, _ := a.Add("demo", AddOpts{Status: "ready"})

	res, err := a.Attach(tk.ID, "clip.mp4", []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	wantLine := "[clip.mp4](assets/" + tk.ID + "-clip.mp4)"
	if res.Line != wantLine {
		t.Errorf("Line = %q, want %q", res.Line, wantLine)
	}
}

func TestAttachUnknownIDIsNotFound(t *testing.T) {
	a := newSeededApp()

	_, err := a.Attach("t-99999", "shot.png", []byte("x"))
	if err == nil {
		t.Fatal("attaching to a missing id must error")
	}
	if got := core.ExitCode(err); got != int(core.CodeNotFound) {
		t.Errorf("exit code = %d, want %d (not-found)", got, core.CodeNotFound)
	}
}
