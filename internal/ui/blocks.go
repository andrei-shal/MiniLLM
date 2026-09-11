package ui

import "strings"

// splitter accumulates streamed markdown and hands out blocks that can no
// longer change: paragraphs closed by a blank line and closed code fences.
// Completed blocks go to the terminal scrollback; only the unfinished tail
// stays in the live area.
type splitter struct {
	buf string
}

// push adds streamed text and returns the blocks it completed.
func (s *splitter) push(text string) []string {
	s.buf += text
	var out []string
	for {
		i := boundary(s.buf)
		if i < 0 {
			return out
		}
		if b := strings.Trim(s.buf[:i], "\n"); strings.TrimSpace(b) != "" {
			out = append(out, b)
		}
		s.buf = s.buf[i:]
	}
}

// pending is the unfinished block.
func (s *splitter) pending() string { return strings.Trim(s.buf, "\n") }

// flush returns the unfinished block and resets the splitter.
func (s *splitter) flush() string {
	b := s.pending()
	s.buf = ""
	if strings.TrimSpace(b) == "" {
		return ""
	}
	return b
}

// boundary returns the offset just past the first complete block in buf, or
// -1. Only newline-terminated lines are considered.
func boundary(buf string) int {
	var (
		off        int
		hasContent bool
		fence      string // opening fence marker while inside a code block
	)
	for {
		nl := strings.IndexByte(buf[off:], '\n')
		if nl < 0 {
			return -1
		}
		line := buf[off : off+nl]
		end := off + nl + 1
		off = end

		trimmed := strings.TrimLeft(line, " ")
		indented := len(line)-len(trimmed) >= 4
		switch {
		case fence != "":
			if !indented && closesFence(trimmed, fence) {
				return end
			}
		case !indented && fenceMarker(trimmed) != "":
			fence = fenceMarker(trimmed)
			hasContent = true
		case strings.TrimSpace(line) == "":
			if hasContent {
				return end
			}
		default:
			hasContent = true
		}
	}
}

// fenceMarker returns the ``` or ~~~ run that opens a code fence, or "".
func fenceMarker(line string) string {
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(line) && line[n] == c {
			n++
		}
		if n >= 3 {
			// A backtick fence's info string can't contain backticks.
			if c == '`' && strings.ContainsRune(line[n:], '`') {
				return ""
			}
			return line[:n]
		}
	}
	return ""
}

func closesFence(line, open string) bool {
	line = strings.TrimRight(line, " \t\r")
	if len(line) < len(open) || line[0] != open[0] {
		return false
	}
	return strings.Trim(line, open[:1]) == ""
}
