package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"minillm/internal/agent"
	"minillm/internal/config"
	"minillm/internal/llm"
)

func (m *Model) View() tea.View {
	if !m.started {
		return tea.NewView("")
	}
	if m.quitting {
		return m.compose("") // the conversation stays on screen, the input goes
	}
	var bottom string
	switch {
	case m.approval != nil:
		bottom = m.approvalView()
	case m.picker != nil:
		bottom = m.picker.view(m.th, m.width, m.height)
	default:
		bottom = m.th.Box.Width(m.width).Render(m.input.View())
	}
	parts := []string{bottom, m.statusLine()}
	if m.picker == nil && m.approval == nil {
		if c := m.completionView(); c != "" {
			parts = append(parts, c)
		}
	}
	if m.busy {
		// The whole frame must stay shorter than the terminal: rows that
		// scroll off the top can't be redrawn and stay in the scrollback.
		fixed := lipgloss.Height(strings.Join(parts, "\n")) + 1 // + frame's spacer
		parts = append([]string{m.liveView(m.height - fixed - 1)}, parts...)
	}
	return m.compose("\n" + strings.Join(parts, "\n")) // a spacer under the conversation
}

// compose stacks the conversation tail on top of below into one frame. Once
// the frame has reached the bottom of the screen it keeps its bottom there:
// rows the tail can't fill are padded at the top, so the input stays put
// when a menu closes.
func (m *Model) compose(below string) tea.View {
	var belowLines []string
	if below != "" {
		belowLines = strings.Split(below, "\n")
	}
	maxH := max(1, m.height-1)
	h := min(len(m.tail)+len(belowLines), maxH)
	if m.top+m.lastH >= m.height {
		h = min(max(h, m.height-m.top), maxH)
	}
	show := max(0, h-len(belowLines))
	vis := m.tail[max(0, len(m.tail)-show):]
	lines := make([]string, 0, h)
	for range show - len(vis) {
		lines = append(lines, "")
	}
	lines = append(append(lines, vis...), belowLines...)

	m.lastH = len(lines)
	if m.top+m.lastH > m.height {
		m.top = m.height - m.lastH // the terminal scrolls
	}
	m.meter.note(m.lastH)
	v := tea.NewView(strings.Join(lines, "\n"))
	v.Cursor = parkedCursor()
	return v
}

// liveView is what sits above the input while a response streams, in at most
// budget lines.
func (m *Model) liveView(budget int) string {
	budget-- // spinner line
	var sections []string
	if m.showThink && !m.thinkDone && m.reasoning.Len() > 0 && budget > 3 {
		n := max(1, budget/3)
		r := wrapStyled(strings.TrimSpace(m.reasoning.String()), m.width-2, m.th.Muted.Italic(true), "  ")
		sections = append(sections, tailLines(r, n))
		budget -= n + 1
	}
	if src := m.split.pending(); src != "" && budget > 1 {
		// Glamour is too slow to run on every token of a long block, so a
		// growing block is re-rendered at most every 50ms. A new block (the
		// previous one went to the scrollback) is rendered right away.
		if src != m.liveSrc && (m.live == "" || !strings.HasPrefix(src, m.liveSrc) || time.Since(m.liveAt) > 50*time.Millisecond) {
			m.live, m.liveSrc, m.liveAt = m.md.render(src, m.width), src, time.Now()
		}
		lines := strings.Split(tailLines(m.live, budget-1), "\n")
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, m.width, "")
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	sections = append(sections, ansi.Truncate(m.spinnerLine(), m.width, ""))
	return strings.Join(sections, "\n\n")
}

func (m *Model) spinnerLine() string {
	label := "жду ответ"
	switch {
	case m.approval != nil:
		label = "ждёт подтверждения"
	case m.gotText:
		label = "пишет"
	case m.reasoning.Len() > 0:
		label = "думает"
	}
	return m.spin.View() + " " + m.th.Accent.Render(label+"…") +
		m.th.Muted.Render(" "+fmtDur(time.Since(m.turnFrom))) +
		m.th.Faint.Render("  esc — стоп")
}

func (m *Model) statusLine() string {
	left := m.th.Accent.Render("●") + " " + m.th.Muted.Render(m.ref())
	if m.agentMode {
		left += "  " + m.th.Warn.Render("⏵⏵ agent")
		if wd, err := os.Getwd(); err == nil {
			left += "  " + m.th.Faint.Render(config.Tilde(wd))
		}
	} else {
		left += "  " + m.th.Faint.Render("chat")
	}
	if t := m.usage.TotalTokens; t > 0 {
		left += "  " + m.th.Faint.Render("ctx "+human(t))
	}
	if m.queued != "" {
		left += "  " + m.th.Warn.Render("⏳ в очереди")
	}

	right := m.th.Faint.Render("/ команды · ⇧⇥ режим · ⌥⏎ строка")
	switch {
	case m.notice != "":
		right = m.th.Warn.Render(m.notice)
	case m.picker != nil:
		right = m.th.Faint.Render("↑↓ выбрать · ⏎ ок · esc отмена")
	case m.approval != nil:
		right = ""
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 2 {
		if m.notice != "" {
			return " " + ansi.Truncate(right, m.width-2, "…")
		}
		return " " + ansi.Truncate(left, m.width-2, "…")
	}
	return " " + left + strings.Repeat(" ", gap) + right
}

func (m *Model) completionView() string {
	comps := m.completions()
	var lines []string
	for i, c := range comps {
		name := fmt.Sprintf("%-10s", c.name)
		line := "   " + m.th.Muted.Render(name) + " " + m.th.Faint.Render(c.desc)
		if i == m.compSel%len(comps) {
			line = " " + m.th.Accent.Render("▸ "+name) + " " + m.th.Muted.Render(c.desc)
		}
		lines = append(lines, ansi.Truncate(line, m.width, "…"))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) approvalView() string {
	a := m.approval
	body := m.th.Warn.Render("Разрешить?  ") + m.toolCallLine(a.Call) + "\n" +
		m.th.Accent.Render("[y]") + " да   " +
		m.th.Accent.Render("[a]") + " всегда для " + a.Call.Function.Name + "   " +
		m.th.Accent.Render("[n]") + " нет"
	return m.th.Box.BorderForeground(m.th.warn).Width(m.width).Render(body)
}

func (m *Model) banner() string {
	return m.th.Accent.Bold(true).Render("◆ MiniLLM") + m.th.Faint.Render(" "+m.version) +
		"\n" + m.th.Muted.Render("  /help — команды · ⇧⇥ — chat/agent · esc — стоп")
}

func (m *Model) userBlock(text string) string {
	lines := strings.Split(wrapStyled(text, m.width-2, m.th.Bold, ""), "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = m.th.Accent.Bold(true).Render("❯ ") + lines[i]
		} else {
			lines[i] = "  " + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Model) thinkingBlock(text string, d time.Duration) string {
	head := "✻ думал"
	if d > 0 {
		head += " " + fmtDur(d)
	}
	if !m.showThink {
		return m.th.Muted.Render(head)
	}
	return m.th.Muted.Render(head) + "\n" + wrapStyled(strings.TrimSpace(text), m.width-2, m.th.Muted.Italic(true), "  ")
}

func (m *Model) toolCallLine(c llm.ToolCall) string {
	return m.th.Accent.Render("⏺ ") + m.th.Bold.Render(c.Function.Name) +
		m.th.Muted.Render("("+summarizeArgs(c.Function.Arguments, max(20, m.width-len(c.Function.Name)-10))+")")
}

func (m *Model) toolResultLine(r agent.ToolResult) string {
	lines := strings.Split(strings.TrimRight(r.Output, "\n"), "\n")
	first := truncate(lines[0], max(20, m.width-20))
	if first == "" {
		first = "(пусто)"
	}
	s := "  ⎿  " + first
	if len(lines) > 1 {
		s += fmt.Sprintf("  (+%d строк)", len(lines)-1)
	}
	if r.Failed {
		return m.th.Error.Render(s)
	}
	return m.th.Muted.Render(s)
}

// summarizeArgs shows a lone string argument as is, anything else as JSON.
func summarizeArgs(args string, n int) string {
	var obj map[string]any
	if json.Unmarshal([]byte(args), &obj) == nil && len(obj) == 1 {
		for _, v := range obj {
			if s, ok := v.(string); ok {
				return truncate(oneLine(s), n)
			}
		}
	}
	return truncate(oneLine(args), n)
}

// transcript renders a stored conversation for the scrollback.
func (m *Model) transcript(msgs []llm.Message) string {
	const maxShown = 30
	var parts []string
	if len(msgs) > maxShown {
		parts = append(parts, "\n"+m.th.Faint.Render(fmt.Sprintf("  … ещё %d сообщений выше", len(msgs)-maxShown)))
		msgs = msgs[len(msgs)-maxShown:]
	}
	for _, msg := range msgs {
		switch msg.Role {
		case llm.RoleUser:
			parts = append(parts, "\n"+m.userBlock(msg.Content))
		case llm.RoleAssistant:
			if msg.Reasoning != "" && m.showThink {
				parts = append(parts, "\n"+m.thinkingBlock(msg.Reasoning, 0))
			}
			if msg.Content != "" {
				parts = append(parts, "\n"+m.md.render(msg.Content, m.width))
			}
			for _, c := range msg.ToolCalls {
				parts = append(parts, "\n"+m.toolCallLine(c))
			}
		case llm.RoleTool:
			parts = append(parts, m.toolResultLine(agent.ToolResult{Output: msg.Content}))
		}
	}
	return strings.Join(parts, "\n")
}
