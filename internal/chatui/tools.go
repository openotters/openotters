package chatui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// toolBlock owns the "current tool" slot(s) of the chat UI. Three
// visual states:
//
//  1. idle      — both slots empty
//  2. running   — spinner line in slot 1, slot 2 empty
//  3. done      — frozen "● tool (input) dur" in slot 1, "⎿ result" in
//     slot 2; stays this way until commit() is called.
type toolBlock struct {
	theme *theme
	sp    spinner.Model

	active    bool
	name      string
	input     string
	startedAt time.Time

	pendingHead string // "● name (input) dur", no leading newline
	pendingBody string // "  ⎿ result"
}

func newToolBlock(t *theme) *toolBlock {
	sp := spinner.New()
	sp.Spinner = spinner.Spinner{
		Frames: []string{"●", "◉", "◎", "○", "◎", "◉"},
		FPS:    time.Second / 8, //nolint:mnd // animation rate
	}
	sp.Style = t.toolDot

	return &toolBlock{theme: t, sp: sp}
}

// onCall marks a tool as running and returns the spinner tick cmd.
func (tb *toolBlock) onCall(name, input string) tea.Cmd {
	tb.active = true
	tb.name = name
	tb.input = input
	tb.startedAt = time.Now()

	return tb.sp.Tick
}

// onResult freezes the running tool into the "done" state.
func (tb *toolBlock) onResult(result string, width int) {
	if !tb.active {
		return
	}

	dur := time.Since(tb.startedAt).Round(time.Millisecond)
	tb.active = false
	tb.pendingHead = tb.renderDone(tb.name, tb.input, dur, width)
	tb.pendingBody = tb.renderResult(result, width)
}

// commit slides the pending tool into scrollback.
func (tb *toolBlock) commit() []tea.Cmd {
	if tb.pendingHead == "" {
		return nil
	}

	head, body := tb.pendingHead, tb.pendingBody
	tb.pendingHead, tb.pendingBody = "", ""

	return []tea.Cmd{
		tea.Println(head),
		tea.Println(body),
	}
}

// reset clears all state (end of turn).
func (tb *toolBlock) reset() {
	tb.active = false
	tb.name, tb.input = "", ""
	tb.pendingHead, tb.pendingBody = "", ""
}

// view returns the (slot1, slot2) strings.
func (tb *toolBlock) view(width int) (string, string) {
	switch {
	case tb.active:
		line := tb.sp.View() + " " + tb.theme.toolName.Render(tb.name)
		if tb.input != "" {
			line += tb.theme.toolInput.Render(
				" (" + truncateTool(tb.input, width-8-len(tb.name)) + ")",
			)
		}

		return line, ""

	case tb.pendingHead != "":
		return tb.pendingHead, tb.pendingBody
	}

	return "", ""
}

// tick forwards a spinner.TickMsg to the underlying spinner.
func (tb *toolBlock) tick(msg spinner.TickMsg) tea.Cmd {
	var cmd tea.Cmd
	tb.sp, cmd = tb.sp.Update(msg)

	return cmd
}

func (tb *toolBlock) renderDone(name, input string, dur time.Duration, width int) string {
	line := tb.theme.toolDotDone.Render("●") + " " + tb.theme.toolName.Render(name)

	if input != "" {
		line += tb.theme.toolInput.Render(" (" + truncateTool(input, width-8-len(name)) + ")")
	}

	if dur > 0 {
		line += " " + tb.theme.dim.Render(dur.String())
	}

	return line
}

const (
	// toolResultMaxLines caps the vertical budget a single tool result
	// is allowed to consume before we fold the tail into a "(+N more
	// lines)" indicator. Big enough to fit a typical `ps` / `ls`
	// table, small enough that a noisy tool (e.g. `otters logs`)
	// can't drown the conversation.
	toolResultMaxLines = 20
)

func (tb *toolBlock) renderResult(result string, width int) string {
	trimmed := strings.TrimRight(result, "\n")
	if strings.TrimSpace(trimmed) == "" {
		return "  " + tb.theme.treeMark.Render("⎿") + " " + tb.theme.toolResult.Render("(no output)")
	}

	lines := strings.Split(trimmed, "\n")

	// Single-line tool output keeps the compact one-liner look.
	if len(lines) == 1 {
		return "  " + tb.theme.treeMark.Render("⎿") + " " +
			tb.theme.toolResult.Render(truncateTool(lines[0], width-4))
	}

	// Multi-line: first row sits after the ⎿ mark, continuation rows
	// get left-padded so the block visually hangs off the marker.
	dropped := 0
	if len(lines) > toolResultMaxLines {
		dropped = len(lines) - toolResultMaxLines
		lines = lines[:toolResultMaxLines]
	}

	var b strings.Builder

	b.WriteString("  " + tb.theme.treeMark.Render("⎿") + " " +
		tb.theme.toolResult.Render(lines[0]))

	for _, line := range lines[1:] {
		b.WriteString("\n    " + tb.theme.toolResult.Render(line))
	}

	if dropped > 0 {
		b.WriteString("\n    " + tb.theme.dim.Render(
			fmt.Sprintf("… (+%d more line", dropped)))

		if dropped != 1 {
			b.WriteString("s")
		}

		b.WriteString(")")
	}

	return b.String()
}

// truncateTool squeezes a tool input/output onto a single line.
// Callers that want multi-line rendering should check for newlines
// first; this helper unconditionally collapses them.
func truncateTool(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)

	if maxLen < 20 {
		maxLen = 20
	}

	if len(s) > maxLen {
		return s[:maxLen-1] + "…"
	}

	return s
}
