package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Bubble Tea v2.0.9's inline renderer leaves stale rows behind when a frame
// shrinks, or the program exits, while the terminal cursor sits low in the
// frame (e.g. on the last line of a tall input). It is reliable with the
// cursor on the first row, so the real cursor is always parked there and
// carets are drawn instead: the textarea's virtual cursor, the picker's block.
func parkedCursor() *tea.Cursor {
	c := tea.NewCursor(0, 0)
	c.Shape = tea.CursorBar
	c.Blink = false
	return c
}

// frame is a standalone frame (the setup wizard's picker): a blank spacer
// line on top holds the parked cursor.
func frame(content string) tea.View {
	v := tea.NewView("\n" + content)
	v.Cursor = parkedCursor()
	return v
}

// frameMeter remembers how tall the frame was recently. Printing above the
// frame moves the cursor up past the new lines and the frame, and a terminal
// can't go above its top row, so each print must fit in the rows that the
// tallest recently drawn frame leaves free.
type frameMeter struct {
	tall  int
	until time.Time
}

func (f *frameMeter) note(height int) {
	if now := time.Now(); height >= f.tall || now.After(f.until) {
		f.tall, f.until = height, now.Add(100*time.Millisecond)
	}
}

// printChunks prints s above the frame in pieces of at most room lines.
func printChunks(s string, room int) tea.Cmd {
	lines := strings.Split(s, "\n")
	room = max(1, room)
	var cmds []tea.Cmd
	for len(lines) > 0 {
		n := min(room, len(lines))
		cmds = append(cmds, tea.Println(strings.Join(lines[:n], "\n")))
		lines = lines[n:]
	}
	return tea.Sequence(cmds...)
}
