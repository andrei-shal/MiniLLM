package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestMarkdownFitsWidth(t *testing.T) {
	md := &markdown{dark: true}
	src := strings.Repeat("слово ", 60) + "\n\n```go\nfmt.Println(1)\n```\n\n- a\n- b"
	for _, width := range []int{40, 60, 200} {
		out := md.render(src, width)
		for _, l := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(l); w > min(width, maxTextWidth) {
				t.Fatalf("width %d: line is %d wide: %q", width, w, ansi.Strip(l))
			}
			if l != strings.TrimRight(l, " ") {
				t.Fatalf("trailing padding left: %q", l)
			}
		}
		if isBlank(strings.Split(out, "\n")[0]) {
			t.Fatalf("leading blank line")
		}
	}
}
