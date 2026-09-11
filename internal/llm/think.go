package llm

import "strings"

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
	spaces     = " \t\r\n"
)

// thinkSplitter routes a leading <think>…</think> section of the content to
// reasoning, for servers that return raw reasoning inline (e.g. llama.cpp
// without a reasoning parser). Tags split across chunks are handled.
type thinkSplitter struct {
	state int // 0: undecided, 1: inside <think>, 2: plain content
	buf   string
	strip bool // drop leading whitespace of the plain content
}

func (t *thinkSplitter) push(s string) (content, reasoning string) {
	t.buf += s
	for {
		switch t.state {
		case 0:
			trimmed := strings.TrimLeft(t.buf, spaces)
			if strings.HasPrefix(trimmed, thinkOpen) {
				t.state, t.buf = 1, trimmed[len(thinkOpen):]
				continue
			}
			if strings.HasPrefix(thinkOpen, trimmed) {
				return // could still become <think>; wait for more
			}
			t.state, t.strip = 2, true
		case 1:
			if i := strings.Index(t.buf, thinkClose); i >= 0 {
				reasoning += t.buf[:i]
				t.state, t.strip, t.buf = 2, true, t.buf[i+len(thinkClose):]
				continue
			}
			keep := partialSuffix(t.buf, thinkClose)
			reasoning += t.buf[:len(t.buf)-keep]
			t.buf = t.buf[len(t.buf)-keep:]
			return
		default:
			out := t.buf
			if t.strip {
				out = strings.TrimLeft(out, spaces)
				t.strip = out == ""
			}
			t.buf = ""
			return content + out, reasoning
		}
	}
}

func (t *thinkSplitter) flush() (content, reasoning string) {
	if t.state == 1 {
		reasoning = t.buf
	} else {
		content = strings.TrimLeft(t.buf, spaces)
	}
	t.buf = ""
	return content, reasoning
}

// partialSuffix returns the length of the longest suffix of s that is a proper
// prefix of tag.
func partialSuffix(s, tag string) int {
	for n := min(len(tag)-1, len(s)); n > 0; n-- {
		if strings.HasSuffix(s, tag[:n]) {
			return n
		}
	}
	return 0
}
