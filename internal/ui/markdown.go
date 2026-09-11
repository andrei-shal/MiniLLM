package ui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// markdown renders with glamour, rebuilding the renderer when the width changes.
type markdown struct {
	dark  bool
	width int
	r     *glamour.TermRenderer
}

const maxTextWidth = 120

func (m *markdown) render(src string, width int) string {
	width = max(20, min(width, maxTextWidth))
	if m.r == nil || m.width != width {
		cfg := styles.DarkStyleConfig
		if !m.dark {
			cfg = styles.LightStyleConfig
		}
		// Spacing between blocks is ours to control.
		cfg.Document.BlockPrefix, cfg.Document.BlockSuffix = "", ""
		r, err := glamour.NewTermRenderer(glamour.WithStyles(cfg), glamour.WithWordWrap(width-4))
		if err != nil {
			return src
		}
		m.r, m.width = r, width
	}
	out, err := m.r.Render(src)
	if err != nil {
		return src
	}
	return tidy(out)
}

var trailingPad = regexp.MustCompile(`(?:\x1b\[[0-9;:]*m| )+$`)

// tidy strips glamour's right padding (it breaks re-wrapping on resize) and
// blank lines around the output.
func tidy(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		if loc := trailingPad.FindStringIndex(l); loc != nil {
			tail := l[loc[0]:]
			l = l[:loc[0]]
			if strings.Contains(tail, "\x1b[") {
				l += "\x1b[0m"
			}
			lines[i] = l
		}
	}
	for len(lines) > 0 && isBlank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && isBlank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func isBlank(l string) bool { return strings.TrimSpace(ansi.Strip(l)) == "" }

// wrapStyled word-wraps plain text and styles every line.
func wrapStyled(s string, width int, st lipgloss.Style, indent string) string {
	lines := strings.Split(ansi.Wrap(s, max(10, width-ansi.StringWidth(indent)), ""), "\n")
	for i, l := range lines {
		lines[i] = indent + st.Render(l)
	}
	return strings.Join(lines, "\n")
}

// fitWidth hard-wraps s so no line is wider than width. Lines the terminal
// wraps by itself confuse the inline renderer when printed above the frame.
func fitWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	return ansi.Hardwrap(s, width, true)
}

// tailLines keeps the last n lines of s.
func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if n <= 1 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func fmtDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func human(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%dk", n/1000)
	}
}

func relTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "только что"
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d дн назад", int(d.Hours()/24))
	default:
		return t.Format("02.01.2006")
	}
}
