package ui

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitter(t *testing.T) {
	src := "Intro para\nstill intro\n\n```go\nfunc a() {\n\n}\n```\nAfter code\n\n\n- item\n\ntail"
	var s splitter
	var got []string
	// Stream in small uneven chunks.
	for i := 0; i < len(src); i += 3 {
		got = append(got, s.push(src[i:min(i+3, len(src))])...)
	}
	want := []string{
		"Intro para\nstill intro",
		"```go\nfunc a() {\n\n}\n```",
		"After code",
		"- item",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blocks:\n%q\nwant\n%q", got, want)
	}
	if rest := s.flush(); rest != "tail" {
		t.Fatalf("rest %q", rest)
	}
	if rest := s.flush(); rest != "" {
		t.Fatalf("flush must reset, got %q", rest)
	}
}

func TestSplitterFences(t *testing.T) {
	var s splitter
	// A ~~~ fence isn't closed by ```, and a longer closing fence is fine.
	got := s.push("~~~\n```\n\nx\n~~~~\n")
	if len(got) != 1 || !strings.HasSuffix(got[0], "~~~~") {
		t.Fatalf("got %q", got)
	}
	// Unclosed fence stays pending.
	if got := s.push("```py\nprint(1)\n\n"); len(got) != 0 {
		t.Fatalf("unclosed fence emitted %q", got)
	}
	if p := s.pending(); p != "```py\nprint(1)" {
		t.Fatalf("pending %q", p)
	}
}
