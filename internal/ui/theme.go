// Package ui is the terminal interface: an inline Bubble Tea program where
// finished output goes to the native scrollback and only the input, status
// and the answer being streamed live at the bottom.
package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

type theme struct {
	dark bool

	accent, warn color.Color

	Accent, Muted, Faint, Error, OK, Warn, Bold lipgloss.Style
	Box, Caret                                  lipgloss.Style
}

func newTheme(dark bool) theme {
	ld := lipgloss.LightDark(dark)
	accent := ld(lipgloss.Color("#7C3AED"), lipgloss.Color("#A78BFA"))
	muted := ld(lipgloss.Color("#6B7280"), lipgloss.Color("#9A9AA8"))
	faint := ld(lipgloss.Color("#A1A1AA"), lipgloss.Color("#5E5E6A"))
	red := ld(lipgloss.Color("#DC2626"), lipgloss.Color("#F87171"))
	green := ld(lipgloss.Color("#059669"), lipgloss.Color("#34D399"))
	yellow := ld(lipgloss.Color("#B45309"), lipgloss.Color("#FBBF24"))
	border := ld(lipgloss.Color("#D4D4D8"), lipgloss.Color("#3F3F46"))

	s := lipgloss.NewStyle
	return theme{
		dark:   dark,
		accent: accent,
		warn:   yellow,
		Accent: s().Foreground(accent),
		Muted:  s().Foreground(muted),
		Faint:  s().Foreground(faint),
		Error:  s().Foreground(red),
		OK:     s().Foreground(green),
		Warn:   s().Foreground(yellow),
		Bold:   s().Bold(true),
		Box:    s().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1),
		Caret:  s().Background(accent),
	}
}
