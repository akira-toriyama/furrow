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

func TestExtractAssetRefs(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"none", "just prose, no assets", nil},
		{"image", "![shot.png](assets/t-1-shot.png)", []string{"t-1-shot.png"}},
		{"link", "[clip.mp4](assets/t-1-clip.mp4)", []string{"t-1-clip.mp4"}},
		{
			"image and link, first-seen order",
			"![a](assets/t-1-a.png)\ntext\n[b](assets/t-1-b.mp4)",
			[]string{"t-1-a.png", "t-1-b.mp4"},
		},
		{
			"dedup repeated ref",
			"![a](assets/t-1-a.png)\n![again](assets/t-1-a.png)",
			[]string{"t-1-a.png"},
		},
		{
			"leading ./ tolerated",
			"![a](./assets/t-1-a.png)",
			[]string{"t-1-a.png"},
		},
		{
			"code fence is ignored",
			"```\n![x](assets/t-1-x.png)\n```\nreal: ![y](assets/t-1-y.png)",
			[]string{"t-1-y.png"},
		},
		{
			"inline code is ignored",
			"docs `![x](assets/t-1-x.png)` then ![y](assets/t-1-y.png)",
			[]string{"t-1-y.png"},
		},
		{
			"wiki link is not an asset ref",
			"see [[t-2]] and ![a](assets/t-1-a.png)",
			[]string{"t-1-a.png"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractAssetRefs(c.body)
			if len(got) != len(c.want) {
				t.Fatalf("ExtractAssetRefs(%q) = %q, want %q", c.body, got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Errorf("ExtractAssetRefs(%q)[%d] = %q, want %q", c.body, i, got[i], c.want[i])
				}
			}
		})
	}
}
