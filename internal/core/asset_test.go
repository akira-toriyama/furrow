package core

import "testing"

func TestSanitizeAssetName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"shot.png", "shot.png"},
		{"my screenshot.png", "my-screenshot.png"},
		{"a  b__c.png", "a-b__c.png"}, // runs collapse, underscore/dot kept
		{"weird*name.mp4", "weird-name.mp4"},
		{"../../etc/passwd", "passwd"},    // POSIX dir stripped
		{`C:\Users\a\pic.PNG`, "pic.PNG"}, // Windows dir stripped
		{"!!!", "file"},                   // all-bad -> fallback
		{"", "file"},                      // empty -> fallback
	}
	for _, c := range cases {
		if got := SanitizeAssetName(c.in); got != c.want {
			t.Errorf("SanitizeAssetName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNextAssetName(t *testing.T) {
	taken := map[string]bool{"t-1-shot.png": true, "t-1-shot-2.png": true}
	pred := func(s string) bool { return taken[s] }

	if got := NextAssetName("t-1-shot.png", pred); got != "t-1-shot-3.png" {
		t.Errorf("collision insert = %q, want t-1-shot-3.png", got)
	}
	if got := NextAssetName("t-1-fresh.png", pred); got != "t-1-fresh.png" {
		t.Errorf("free name = %q, want t-1-fresh.png", got)
	}
	takenNoExt := func(s string) bool { return s == "t-1-README" }
	if got := NextAssetName("t-1-README", takenNoExt); got != "t-1-README-2" {
		t.Errorf("no-extension collision = %q, want t-1-README-2", got)
	}
}

func TestAttachLine(t *testing.T) {
	if got := AttachLine("shot.png", "assets/t-1-shot.png"); got != "![shot.png](assets/t-1-shot.png)" {
		t.Errorf("image line = %q", got)
	}
	if got := AttachLine("clip.mp4", "assets/t-1-clip.mp4"); got != "[clip.mp4](assets/t-1-clip.mp4)" {
		t.Errorf("video line = %q", got)
	}
	// extension match is case-insensitive
	if got := AttachLine("SHOT.PNG", "assets/t-1-SHOT.PNG"); got != "![SHOT.PNG](assets/t-1-SHOT.PNG)" {
		t.Errorf("uppercase image line = %q", got)
	}
}

func TestAssetPathAndRef(t *testing.T) {
	if got := AssetPath("t-1-shot.png"); got != "bodies/assets/t-1-shot.png" {
		t.Errorf("AssetPath = %q", got)
	}
	if got := AssetRef("t-1-shot.png"); got != "assets/t-1-shot.png" {
		t.Errorf("AssetRef = %q", got)
	}
}
