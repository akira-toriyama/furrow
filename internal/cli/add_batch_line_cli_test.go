package cli

import (
	"strings"
	"testing"
)

// t-sza0: a bulk add's validation errors name the failing record by the
// coordinate its parse errors already print — the physical input line, blank
// lines counted. They used to say "spec N", the 0-based index of the non-blank
// lines, a word no help defines and a number that drifts from the line as
// soon as the input carries a blank line.
func TestCLIBulkAddErrorsNamePhysicalLine(t *testing.T) {
	initStore(t)
	cases := []struct {
		name  string
		stdin string
		args  []string
		want  string
	}{
		{
			name:  "--batch record after a blank line",
			stdin: "\n" + `{"title":"k5","repeat":"weekly"}` + "\n",
			args:  []string{"add", "--batch", "-"},
			want:  `--batch line 2 ("k5"): --repeat needs a --due`,
		},
		{
			name:  "--batch duplicate key names both lines",
			stdin: `{"key":"a","title":"first"}` + "\n\n" + `{"key":"a","title":"second"}` + "\n",
			args:  []string{"add", "--batch", "-"},
			want:  `--batch line 3 ("second"): key "a" is already used by line 1 ("first")`,
		},
		{
			name:  "--stdin title after a blank first line",
			stdin: "\nalpha\n",
			args:  []string{"add", "--stdin", "-s", "ghots"},
			want:  `--stdin line 2 ("alpha"): unknown lane`,
		},
		{
			name:  "--batch empty title after a blank line",
			stdin: "\n" + `{"title":"\u0007"}` + "\n",
			args:  []string{"add", "--batch", "-"},
			want:  `--batch line 2 has an empty title`,
		},
		{
			name:  "--stdin empty title after a blank line",
			stdin: "ok\n\n\x1b\n",
			args:  []string{"add", "--stdin"},
			want:  `--stdin line 3 has an empty title`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fe, _ := runErrIn(t, c.stdin, c.args...)
			if fe == nil || exitOf(fe) != 2 || !strings.Contains(fe.Msg, c.want) {
				t.Fatalf("want exit 2 naming %q, got %+v", c.want, fe)
			}
		})
	}
}
