package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const historyLimit = 1000

// inputHistory is shell-like ↑/↓ recall, persisted as one JSON string per line.
type inputHistory struct {
	path  string
	items []string
	pos   int // len(items) when not browsing
	draft string
}

func loadHistory(path string) *inputHistory {
	h := &inputHistory{path: path}
	if raw, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			var s string
			if json.Unmarshal([]byte(line), &s) == nil && s != "" {
				h.items = append(h.items, s)
			}
		}
		if len(h.items) > 2*historyLimit {
			h.items = h.items[len(h.items)-historyLimit:]
			h.rewrite()
		}
	}
	h.pos = len(h.items)
	return h
}

func (h *inputHistory) add(s string) {
	defer h.reset()
	if n := len(h.items); n > 0 && h.items[n-1] == s {
		return
	}
	h.items = append(h.items, s)
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(s)
	f.Write(append(b, '\n'))
}

func (h *inputHistory) rewrite() {
	var b strings.Builder
	for _, s := range h.items {
		j, _ := json.Marshal(s)
		b.Write(j)
		b.WriteByte('\n')
	}
	os.WriteFile(h.path, []byte(b.String()), 0o600)
}

func (h *inputHistory) reset() { h.pos, h.draft = len(h.items), "" }

// prev steps back, remembering the unsent draft on the first step.
func (h *inputHistory) prev(current string) (string, bool) {
	if h.pos == 0 {
		return "", false
	}
	if h.pos == len(h.items) {
		h.draft = current
	}
	h.pos--
	return h.items[h.pos], true
}

func (h *inputHistory) next() (string, bool) {
	if h.pos >= len(h.items) {
		return "", false
	}
	h.pos++
	if h.pos == len(h.items) {
		return h.draft, true
	}
	return h.items[h.pos], true
}
