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
//  2. running   — spinner line in slot 1, slot 2 empty (or a live
//     job-log preview when the running tool is a job-observation
//     call like job_watch / job_wait / job_status)
//  3. done      — frozen "● tool (input) dur" in slot 1, "⎿ result"
//     in slot 2; stays this way until commit() is called.
type toolBlock struct {
	theme *theme
	sp    spinner.Model

	active    bool
	name      string
	input     string
	startedAt time.Time

	pendingHead string // "● name (input) dur", no leading newline
	pendingBody string // "  ⎿ result"

	// Live job-log state, populated while the active tool is one of
	// jobObservationTools and the model has started polling
	// GetAsyncJob. Cleared on reset / onResult — once the tool
	// returns, the frozen result body becomes the canonical view of
	// what the model saw.
	jobActive bool
	jobID     string
	jobStdout string
	jobStderr string
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
	tb.jobActive = false
	tb.jobID = ""
	tb.jobStdout = ""
	tb.jobStderr = ""

	return tb.sp.Tick
}

// beginJobWatch marks the active tool as a job-observation call so
// view() renders a live-log preview alongside the spinner. Called
// from the model right after onCall when the tool name + input
// resolve to a real job_id; without this the spinner alone is the
// only affordance for long waits.
func (tb *toolBlock) beginJobWatch(jobID string) {
	if !tb.active {
		return
	}

	tb.jobActive = true
	tb.jobID = jobID
	tb.jobStdout = ""
	tb.jobStderr = ""
}

// applyJobUpdate refreshes the live-log buffers with the latest
// snapshot. Drops stale updates (the jobID in the message no longer
// matches the active watch — happens on rapid tool transitions).
func (tb *toolBlock) applyJobUpdate(jobID, stdout, stderr string) {
	if !tb.jobActive || tb.jobID != jobID {
		return
	}

	tb.jobStdout = stdout
	tb.jobStderr = stderr
}

// activeJobID returns the currently-watched job's id, or "" when no
// watch is in flight. Used by the model to decide whether a
// jobLogUpdateMsg is still relevant.
func (tb *toolBlock) activeJobID() string {
	if !tb.jobActive {
		return ""
	}

	return tb.jobID
}

// onResult freezes the running tool into the "done" state.
func (tb *toolBlock) onResult(result string, width int) {
	if !tb.active {
		return
	}

	dur := time.Since(tb.startedAt).Round(time.Millisecond)
	tb.active = false
	tb.jobActive = false
	tb.jobID = ""
	tb.jobStdout = ""
	tb.jobStderr = ""
	tb.pendingHead = tb.renderDone(tb.name, tb.input, dur, width)
	tb.pendingBody = tb.renderResult(tb.name, result, width)
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
	tb.jobActive = false
	tb.jobID = ""
	tb.jobStdout = ""
	tb.jobStderr = ""
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

		// Elapsed-time counter — only show once the tool has been
		// running long enough that the user might wonder. Below 5 s
		// it's noise; the spinner alone is enough affordance.
		if elapsed := time.Since(tb.startedAt); elapsed >= 5*time.Second {
			line += " " + tb.theme.dim.Render(fmtElapsedSeconds(elapsed))
		}

		// Live job-log preview goes in slot2 when a job-observation
		// tool is in flight; the result body slot is the right home
		// for it (same vertical position the frozen result will
		// occupy once the tool returns, so the layout doesn't
		// shift).
		if tb.jobActive {
			return line, tb.renderJobLogPreview(width)
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

	// jobWatchResultMaxLines is the loosened cap applied specifically
	// to job_watch — its entire purpose is to surface a BIN's stdout,
	// so the generic 20-line ceiling chops off the meat. 100 lines
	// covers most "kubectl rollout status" / build-log style payloads
	// without letting a runaway BIN drown a 30-line terminal.
	jobWatchResultMaxLines = 100

	// jobLogPreviewLines is how many tail lines of stdout (and stderr)
	// the live-log preview shows beneath the spinner while a
	// job-observation tool is in flight. Two non-overlapping panes
	// times this many lines is the vertical budget the active area
	// can grow to without pushing the prompt off-screen on a small
	// terminal.
	jobLogPreviewLines = 6
)

// resultMaxLines returns the per-tool cap used by renderResult.
// Most tools share the conservative default; job_watch gets a
// loosened cap because its stdout is the entire payload. Add cases
// here as new tools join the "stdout is the answer" pattern.
func resultMaxLines(toolName string) int {
	if toolName == "job_watch" {
		return jobWatchResultMaxLines
	}

	return toolResultMaxLines
}

func (tb *toolBlock) renderResult(toolName, result string, width int) string {
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
	maxLines := resultMaxLines(toolName)

	dropped := 0
	if len(lines) > maxLines {
		dropped = len(lines) - maxLines
		lines = lines[:maxLines]
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

// renderJobLogPreview renders the live stdout/stderr tail beneath
// the spinner row for a job-observation tool. Shows the last
// jobLogPreviewLines of each stream, separated by labels. Empty
// buffers get an "(waiting for output…)" placeholder so the pane
// is always visible — the affordance is *the panel existing*, not
// just its content.
func (tb *toolBlock) renderJobLogPreview(width int) string {
	if tb.jobStdout == "" && tb.jobStderr == "" {
		return "  " + tb.theme.treeMark.Render("⎿") + " " +
			tb.theme.dim.Render("waiting for output from "+tb.jobID+"…")
	}

	var b strings.Builder

	if tb.jobStdout != "" {
		b.WriteString("  " + tb.theme.treeMark.Render("⎿ stdout"))

		for _, line := range tailLines(tb.jobStdout, jobLogPreviewLines) {
			b.WriteString("\n    " + tb.theme.toolResult.Render(
				truncateTool(line, width-4),
			))
		}
	}

	if tb.jobStderr != "" {
		if tb.jobStdout != "" {
			b.WriteString("\n")
		}

		b.WriteString("  " + tb.theme.treeMark.Render("⎿ stderr"))

		for _, line := range tailLines(tb.jobStderr, jobLogPreviewLines) {
			b.WriteString("\n    " + tb.theme.toolResult.Render(
				truncateTool(line, width-4),
			))
		}
	}

	return b.String()
}

// tailLines returns the last n lines of s, trimming the trailing
// newline first so an empty final line doesn't eat one of the n
// slots. n <= 0 returns the empty slice.
func tailLines(s string, n int) []string {
	if n <= 0 || s == "" {
		return nil
	}

	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return lines
	}

	return lines[len(lines)-n:]
}

// fmtElapsedSeconds formats a duration as the compact "12s" /
// "1m04s" / "1h02m" style. Used for the live elapsed counter on
// running tool rows — re-rendered each spinner tick so the user
// sees seconds advance. Below 60 s shows just seconds; above
// surfaces minutes for readability.
func fmtElapsedSeconds(d time.Duration) string {
	secs := int(d.Seconds())

	switch {
	case secs < 60: //nolint:mnd // human-friendly thresholds
		return fmt.Sprintf("%ds", secs)
	case secs < 3600: //nolint:mnd // 1h
		return fmt.Sprintf("%dm%02ds", secs/60, secs%60) //nolint:mnd // minute math
	default:
		return fmt.Sprintf("%dh%02dm", secs/3600, (secs%3600)/60) //nolint:mnd // hour math
	}
}

// truncateTool squeezes a tool input/output onto a single line.
// Callers that want multi-line rendering should check for newlines
// first; this helper unconditionally collapses them.
func truncateTool(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)

	if maxLen < 20 { //nolint:mnd // floor below which truncation is unreadable
		maxLen = 20
	}

	if len(s) > maxLen {
		return s[:maxLen-1] + "…"
	}

	return s
}
