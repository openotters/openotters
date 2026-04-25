package chatui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// theme bundles every lipgloss.Style the chat UI uses.
type theme struct {
	prompt       lipgloss.Style
	userDot      lipgloss.Style
	agentDot     lipgloss.Style
	infoDot      lipgloss.Style
	errorDot     lipgloss.Style
	toolDot      lipgloss.Style
	toolDotDone  lipgloss.Style
	toolDotError lipgloss.Style
	treeMark     lipgloss.Style
	toolName     lipgloss.Style
	toolInput    lipgloss.Style
	toolResult   lipgloss.Style
	errText      lipgloss.Style
	dim          lipgloss.Style
	banner       lipgloss.Style
}

func defaultTheme() *theme {
	mk := func(fg string, bold bool) lipgloss.Style {
		s := lipgloss.NewStyle().Foreground(lipgloss.Color(fg))
		if bold {
			s = s.Bold(true)
		}

		return s
	}

	return &theme{
		prompt:       mk("213", true),
		userDot:      mk("213", true),
		agentDot:     mk("117", true),
		infoDot:      mk("75", true),
		errorDot:     mk("203", true),
		toolDot:      mk("214", true),
		toolDotDone:  mk("108", true),
		toolDotError: mk("203", true),
		treeMark:     mk("244", false),
		toolName:     lipgloss.NewStyle().Bold(true),
		toolInput:    mk("244", false),
		toolResult:   mk("244", false),
		errText:      mk("203", false),
		dim:          mk("244", false),
		banner:       mk("114", true),
	}
}

// renderRule draws a dim horizontal rule spanning width.
func (t *theme) renderRule(width int) string {
	w := width
	if w < 4 {
		w = 4
	}

	return t.dim.Render(strings.Repeat("─", w))
}

// renderStatusBar builds a "left … right" status strip with a filler gap
// sized to the given width.
func (t *theme) renderStatusBar(width int, left, right string) string {
	w := width
	if w < 20 {
		w = 20
	}

	gap := w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 2 {
		gap = 2
	}

	return " " + t.dim.Render(left) + strings.Repeat(" ", gap) + t.dim.Render(right) + " "
}

// fmtElapsed formats a duration as a compact status value.
func fmtElapsed(d time.Duration) string {
	return fmt.Sprint(d.Round(time.Second))
}
