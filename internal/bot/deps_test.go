package bot

import (
	"strings"
	"testing"
)

func TestSafeMD_EscapesMarkdownSpecials(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hello", "hello"},
		{"under_score", "under\\_score"},
		{"*bold*", "\\*bold\\*"},
		{"`code`", "\\`code\\`"},
		{"[link]", "\\[link]"},
		{"_*`[mixed]", "\\_\\*\\`\\[mixed]"},
		{"", ""},
	}
	for _, c := range cases {
		got := SafeMD(c.in)
		if got != c.want {
			t.Errorf("SafeMD(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// FuzzSafeMD makes sure SafeMD never panics and never leaves a bare Markdown
// special in its output. A bare special means Telegram would interpret it
// as formatting — which is exactly the injection vector this function blocks.
func FuzzSafeMD(f *testing.F) {
	seeds := []string{"", "hello", "_*`[", "用户 name", "🔥 *bold*", "\\\\_already escaped"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := SafeMD(in)
		// Every dangerous rune in the output must be preceded by a backslash.
		for i, r := range out {
			if !isMDSpecial(r) {
				continue
			}
			if i == 0 || !strings.HasSuffix(out[:i], "\\") {
				t.Fatalf("unescaped Markdown special %q at %d in output %q (input %q)", r, i, out, in)
			}
		}
	})
}

func isMDSpecial(r rune) bool {
	switch r {
	case '_', '*', '`', '[':
		return true
	}
	return false
}
