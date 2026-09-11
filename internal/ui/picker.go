package ui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type pickItem struct {
	Label string // shown and matched
	Hint  string
	Value string
}

// picker is a fuzzy-filtered list that replaces the input box.
type picker struct {
	kind    string // "model", "provider", "session"
	only    string // model picker: restrict to this provider
	title   string
	items   []pickItem
	filter  string
	sel     int
	loading int
	errs    []string

	onPick   func(pickItem) tea.Cmd
	onCustom func(text string) tea.Cmd // Enter with no match uses the typed text
}

const pickerRows = 10

func (p *picker) addModels(provider string, models []string, current string) {
	for _, id := range models {
		ref := provider + "/" + id
		it := pickItem{Label: ref, Value: ref}
		if ref == current {
			it.Hint = "текущая"
			if p.filter == "" {
				p.sel = len(p.items)
			}
		}
		p.items = append(p.items, it)
	}
}

func (p *picker) visible() []pickItem {
	if p.filter == "" {
		return p.items
	}
	q := strings.ToLower(p.filter)
	type scored struct {
		it    pickItem
		score int
	}
	var ss []scored
	for _, it := range p.items {
		if s, ok := fuzzy(strings.ToLower(it.Label), q); ok {
			ss = append(ss, scored{it, s})
		}
	}
	slices.SortStableFunc(ss, func(a, b scored) int { return cmp.Compare(a.score, b.score) })
	out := make([]pickItem, len(ss))
	for i, s := range ss {
		out[i] = s.it
	}
	return out
}

// fuzzy matches q as a substring (best) or a subsequence of s; lower is better.
func fuzzy(s, q string) (int, bool) {
	if i := strings.Index(s, q); i >= 0 {
		return i, true
	}
	score, at := 1000, 0
	for _, r := range q {
		j := strings.IndexRune(s[at:], r)
		if j < 0 {
			return 0, false
		}
		score += j
		at += j + utf8.RuneLen(r)
	}
	return score, true
}

func (p *picker) type_(s string) {
	p.filter += s
	p.sel = 0
}

// key handles a key press; done means the picker should close.
func (p *picker) key(k tea.KeyPressMsg) (cmd tea.Cmd, done bool) {
	vis := p.visible()
	switch k.String() {
	case "esc", "ctrl+c":
		return nil, true
	case "up", "ctrl+p", "shift+tab":
		if p.sel > 0 {
			p.sel--
		}
	case "down", "ctrl+n", "tab":
		if p.sel < len(vis)-1 {
			p.sel++
		}
	case "enter":
		if len(vis) > 0 {
			return p.onPick(vis[min(p.sel, len(vis)-1)]), true
		}
		if text := strings.TrimSpace(p.filter); text != "" && p.onCustom != nil {
			return p.onCustom(text), true
		}
	case "backspace", "ctrl+h":
		if r := []rune(p.filter); len(r) > 0 {
			p.filter = string(r[:len(r)-1])
			p.sel = 0
		}
	case "ctrl+u":
		p.filter, p.sel = "", 0
	default:
		if k.Text != "" {
			p.type_(k.Text)
		}
	}
	return nil, false
}

func (p *picker) view(th theme, width, height int) string {
	inner := max(10, width-4)
	lines := []string{th.Accent.Bold(true).Render(p.title) + "  " + th.Accent.Render("❯ ") + p.filter + th.Caret.Render(" ")}

	// Keep the frame shorter than the terminal (title, borders, status, notes).
	rows := max(3, min(pickerRows, height-8-len(p.errs)))
	vis := p.visible()
	sel := min(p.sel, max(0, len(vis)-1))
	start := max(0, min(sel-rows/2, len(vis)-rows))
	for i := start; i < min(len(vis), start+rows); i++ {
		it := vis[i]
		line := "  " + it.Label
		if i == sel {
			line = th.Accent.Render("▸ " + it.Label)
		}
		if it.Hint != "" {
			line += "  " + th.Faint.Render(it.Hint)
		}
		lines = append(lines, ansi.Truncate(line, inner, "…"))
	}
	switch {
	case len(vis) == 0 && p.loading > 0:
	case len(vis) == 0 && p.onCustom != nil && strings.TrimSpace(p.filter) != "":
		lines = append(lines, th.Muted.Render("  ⏎ использовать «"+strings.TrimSpace(p.filter)+"»"))
	case len(vis) == 0:
		lines = append(lines, th.Muted.Render("  ничего не найдено"))
	case len(vis) > rows:
		lines = append(lines, th.Faint.Render("  … всего "+strconv.Itoa(len(vis))))
	}
	if p.loading > 0 {
		lines = append(lines, th.Muted.Render("  загружаю список моделей…"))
	}
	for _, e := range p.errs {
		lines = append(lines, ansi.Truncate(th.Error.Render("  ✗ "+e), inner, "…"))
	}
	return th.Box.BorderForeground(th.accent).Width(width).Render(strings.Join(lines, "\n"))
}

// pickProgram runs a picker on its own, for the setup wizard.
type pickProgram struct {
	p             *picker
	th            theme
	width, height int
	done          bool
}

func (m *pickProgram) Init() tea.Cmd { return nil }

func (m *pickProgram) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.PasteMsg:
		m.p.type_(oneLine(msg.Content))
	case tea.KeyPressMsg:
		if _, done := m.p.key(msg); done {
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *pickProgram) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	return frame(m.p.view(m.th, max(40, m.width), max(12, m.height)))
}

// Pick shows a standalone picker and returns the chosen (or typed, if custom)
// value, or "" if cancelled.
func Pick(title string, values []string, dark, custom bool) (string, error) {
	var chosen string
	p := &picker{title: title, onPick: func(it pickItem) tea.Cmd { chosen = it.Value; return nil }}
	if custom {
		p.onCustom = func(text string) tea.Cmd { chosen = text; return nil }
	}
	for _, v := range values {
		p.items = append(p.items, pickItem{Label: v, Value: v})
	}
	if _, err := tea.NewProgram(&pickProgram{p: p, th: newTheme(dark), width: 80, height: 24}).Run(); err != nil {
		return "", err
	}
	return chosen, nil
}
