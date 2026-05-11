package chatui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// tickMsg refreshes the elapsed field on the status bar every second.
type tickMsg time.Time

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// model composes the chat UI. Each field (theme, tools, text, slash…)
// owns a single concern; Update orchestrates them.
type model struct {
	cfg    Config
	client daemonv1.RuntimeClient
	parent context.Context //nolint:containedctx // tea runs outside request ctx

	theme *theme
	input textinput.Model

	tools *toolBlock
	text  *textBlock
	slash *slashRegistry
	hist  *history

	width, height int
	startedAt     time.Time
	turns         int

	streaming    bool
	streamCancel context.CancelFunc
	flash        string

	// runningJobs is the latest session-scoped snapshot of in-flight
	// jobs, refreshed every runningJobsPollInterval. Rendered as a
	// strip below the status bar so the user always knows what work
	// the agent has running in the background.
	runningJobs []runningJobSnapshot
}

func newModel(ctx context.Context, cfg Config, rc daemonv1.RuntimeClient) *model {
	th := defaultTheme()

	ti := textinput.New()
	ti.Placeholder = "message the agent — / for commands, ? for help"
	ti.Prompt = ""
	ti.Focus()
	ti.CharLimit = 0

	return &model{
		cfg:       cfg,
		client:    rc,
		parent:    ctx,
		theme:     th,
		input:     ti,
		tools:     newToolBlock(th),
		text:      newTextBlock(th),
		slash:     newSlashRegistry(ctx, th, func() string { return cfg.SessionID }, rc),
		hist:      newHistory(500), //nolint:mnd // ring cap
		startedAt: time.Now(),
	}
}

// ─────────────────────────────────────────────────────────────────────
// bubbletea lifecycle

func (m *model) Init() tea.Cmd {
	banner := m.theme.banner.Render(fmt.Sprintf("otters ▸ %s", m.cfg.Ref))
	hint := m.theme.dim.Render(fmt.Sprintf(
		"session: %s — / for commands, Ctrl+C to exit", m.cfg.SessionID))

	return tea.Batch(
		textinput.Blink,
		tickEvery(time.Second),
		tea.Sequence(tea.Println(banner), tea.Println(hint)),
		loadHistory(m.parent, m.client, m.cfg.Ref, m.cfg.SessionID),
		pollRunningJobs(m.parent, m.client, m.cfg.SessionID),
	)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.Width = msg.Width - 3

		return m, nil

	case tea.KeyMsg:
		return m.onKey(msg)

	case tickMsg:
		return m, tickEvery(time.Second)

	case spinner.TickMsg:
		return m, m.tools.tick(msg)

	case streamStartedMsg:
		m.streamCancel = msg.cancel

		return m, recvNext(msg.stream)

	case streamEventMsg:
		return m.onEvent(msg)

	case streamDoneMsg:
		return m.onStreamDone()

	case streamErrorMsg:
		return m.onStreamError(msg)

	case jobLogUpdateMsg:
		return m.onJobLogUpdate(msg)

	case runningJobsMsg:
		// Drop the snapshot if the RPC errored — keep the last
		// good state so a flaky daemon doesn't make the bar
		// blink empty. Re-arm regardless.
		if msg.err == nil {
			m.runningJobs = msg.jobs
		}

		return m, pollRunningJobs(m.parent, m.client, m.cfg.SessionID)

	case slashOutputMsg:
		// Slash commands fire async RPCs (ListAsyncJobs, GetAsyncJob,
		// CancelAsyncJob) and return their rendered output via this
		// message. Push it into scrollback so the user sees a
		// stable line in the transcript.
		return m, tea.Println(msg.text)

	case historyLoadedMsg:
		m.hist.preload(msg.prompts)

		return m, nil

	case historyLoadErrorMsg:
		// Absence of history isn't fatal (new session, stopped agent,
		// missing RPC) — log nothing, continue with empty history.
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)

	return m, cmd
}

// ─────────────────────────────────────────────────────────────────────
// View

func (m *model) View() string {
	slot1, slot2 := m.tools.view(m.innerWidth())

	lines := []string{
		slot1,
		slot2,
		m.theme.renderRule(m.innerWidth()),
		m.theme.prompt.Render("›") + " " + m.input.View(),
		m.theme.renderRule(m.innerWidth()),
		m.renderStatusBar(),
	}

	// Bottom strip with currently-running jobs scoped to this
	// session. Hidden entirely when nothing is in flight so the
	// chrome stays minimal on a quiet conversation.
	if strip := m.renderRunningJobsStrip(); strip != "" {
		lines = append(lines, strip)
	}

	return strings.Join(lines, "\n")
}

// renderRunningJobsStrip formats the in-flight jobs as a single
// line below the status bar. Shape:
//
//	running 2: ● job_abc yaegi 12s · ● job_def curl 1m04s
//
// Single-line because the bottom of the screen is a tight visual
// budget — multi-line would push the prompt up and disrupt the
// "input always near the cursor" reading flow. When the
// concatenated line would exceed the terminal width, jobs are
// dropped from the right and replaced with a "+N more" suffix.
// Returns the empty string when nothing's running so the View()
// drops the strip entirely.
func (m *model) renderRunningJobsStrip() string {
	if len(m.runningJobs) == 0 {
		return ""
	}

	width := m.innerWidth()
	prefix := fmt.Sprintf("running %d:", len(m.runningJobs))

	// Build entries (one per job) in styled form. Track the
	// unstyled (display) length alongside so we can budget against
	// the terminal width without measuring ANSI codes.
	type entry struct {
		styled string
		plain  string
	}

	entries := make([]entry, 0, len(m.runningJobs))

	for _, j := range m.runningJobs {
		age := fmtElapsedSeconds(time.Since(j.CreatedAt).Round(time.Second))
		plain := fmt.Sprintf("● %s %s %s", j.ID, j.Bin, age)
		styled := m.theme.toolDot.Render("●") + " " +
			m.theme.toolName.Render(j.ID) + " " +
			m.theme.toolInput.Render(j.Bin) + " " +
			m.theme.dim.Render(age)
		entries = append(entries, entry{styled: styled, plain: plain})
	}

	// Greedy fit: append entries until we'd exceed `width`,
	// reserving 12 chars for a "… +N more" suffix.
	const moreReserve = 12
	const separator = " · "

	used := len(prefix) + 1 // " " between prefix and first entry
	kept := 0

	for _, e := range entries {
		add := len(e.plain)
		if kept > 0 {
			add += len(separator)
		}

		// Leave room for the "+N more" tail when there are still
		// entries after this one.
		budget := width
		if kept+1 < len(entries) {
			budget -= moreReserve
		}

		if used+add > budget {
			break
		}

		used += add
		kept++
	}

	if kept == 0 {
		// Nothing fits even with truncation; show the count alone.
		return m.theme.dim.Render(fmt.Sprintf("running: %d jobs (window too narrow)", len(entries)))
	}

	var b strings.Builder

	b.WriteString(m.theme.dim.Render(prefix))
	b.WriteString(" ")

	for i := 0; i < kept; i++ {
		if i > 0 {
			b.WriteString(m.theme.dim.Render(separator))
		}

		b.WriteString(entries[i].styled)
	}

	if kept < len(entries) {
		b.WriteString(" ")
		b.WriteString(m.theme.dim.Render(fmt.Sprintf("+%d more", len(entries)-kept)))
	}

	return b.String()
}

func (m *model) renderStatusBar() string {
	left := "/ or ? for commands · ^C to exit"
	if m.streaming {
		left = "^C to abort reply"
	}

	if m.flash != "" {
		left = m.flash
	}

	right := fmt.Sprintf("turns: %d · %s", m.turns, fmtElapsed(time.Since(m.startedAt)))

	return m.theme.renderStatusBar(m.innerWidth(), left, right)
}

func (m *model) innerWidth() int {
	if m.width > 0 {
		return m.width
	}

	if w, _, err := term.GetSize(0); err == nil && w > 0 {
		return w
	}

	return 80
}

// ─────────────────────────────────────────────────────────────────────
// Key handling

func (m *model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type { //nolint:exhaustive // only relevant keys
	case tea.KeyCtrlC, tea.KeyEsc:
		if m.streaming {
			if m.streamCancel != nil {
				m.streamCancel()
			}

			m.flash = "(aborting…)"

			return m, nil
		}

		return m, tea.Quit

	case tea.KeyEnter:
		if m.streaming {
			return m, nil
		}

		return m.onSubmit(strings.TrimSpace(m.input.Value()))

	case tea.KeyUp:
		if m.streaming {
			return m, nil
		}

		m.input.SetValue(m.hist.prev(m.input.Value()))
		m.input.CursorEnd()

		return m, nil

	case tea.KeyDown:
		if m.streaming {
			return m, nil
		}

		m.input.SetValue(m.hist.next())
		m.input.CursorEnd()

		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)

	return m, cmd
}

func (m *model) onSubmit(text string) (tea.Model, tea.Cmd) {
	if text == "" {
		return m, nil
	}

	// Append every submission (chat + slash) to history so Up recalls
	// them within the session. Slash commands don't persist on the
	// runtime, so they live for this chat invocation only.
	m.hist.append(text)
	m.hist.reset()

	m.input.Reset()
	m.flash = ""

	if text == "/" || text == "?" {
		return m, m.slash.Help()
	}

	if strings.HasPrefix(text, "/") {
		cmd, quit := m.slash.Dispatch(text)
		if quit {
			return m, tea.Quit
		}

		return m, cmd
	}

	m.turns++
	m.streaming = true
	m.text.reset()
	m.tools.reset()

	req := &daemonv1.ChatStreamRequest{
		Ref:       m.cfg.Ref,
		SessionId: m.cfg.SessionID,
		Prompt:    text,
	}

	return m, tea.Batch(
		tea.Println("\n"+m.theme.userDot.Render("●")+" "+text),
		openStream(m.parent, m.client, req),
	)
}

// ─────────────────────────────────────────────────────────────────────
// Stream event routing

func (m *model) onEvent(msg streamEventMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg.event.GetType() {
	case "text.delta":
		cmds = append(cmds, m.tools.commit()...)
		cmds = append(cmds, m.text.feed(msg.event.GetContent())...)

	case "tool.call":
		cmds = append(cmds, m.text.flush()...)
		cmds = append(cmds, m.tools.commit()...)
		cmds = append(cmds, tea.Println(""))
		m.text.endBlock()

		toolName := msg.event.GetTool()
		toolInput := unwrapToolField(msg.event.GetContent(), "input")

		cmds = append(cmds, m.tools.onCall(toolName, toolInput))

		// For job-observation tools (job_watch / job_wait / job_status)
		// kick off a 1s poller against GetAsyncJob so the spinner row
		// grows a live stdout/stderr tail beneath it. Order matters:
		// onCall resets the toolBlock state (including any prior
		// jobActive flag), so beginJobWatch must run AFTER it.
		if isJobObservationTool(toolName) {
			if jobID := extractJobID(toolInput); jobID != "" {
				m.tools.beginJobWatch(jobID)
				cmds = append(cmds, pollJobLogs(m.parent, m.client, jobID))
			}
		}

	case "tool.result":
		m.tools.onResult(
			unwrapToolField(msg.event.GetContent(), "output"),
			m.innerWidth(),
		)

	case "step.finish":
		cmds = append(cmds, m.text.flush()...)
		m.text.endBlock()

	case "message.create":
		// Duplicate of final step.finish — already rendered.

	case "error":
		cmds = append(cmds, m.tools.commit()...)
		cmds = append(cmds, tea.Println("\n"+
			m.theme.errorDot.Render("●")+" "+
			m.theme.errText.Render("error: ")+msg.event.GetContent()))
	}

	cmds = append(cmds, recvNext(msg.stream))

	return m, tea.Sequence(cmds...)
}

// onJobLogUpdate folds a freshly-fetched job snapshot into the
// active tool block's live-log buffers and decides whether to keep
// polling. Stops polling on terminal status or when the active
// watch has moved on (e.g. the tool already returned and the
// toolBlock cleared its jobID). Errors are silently swallowed —
// the affordance is best-effort and a flaky GetAsyncJob shouldn't
// blow up the chat with a red banner.
func (m *model) onJobLogUpdate(msg jobLogUpdateMsg) (tea.Model, tea.Cmd) {
	// Stale message — the tool already finished or moved to a
	// different job. Drop without re-arming.
	if m.tools.activeJobID() != msg.jobID {
		return m, nil
	}

	if msg.err == nil {
		m.tools.applyJobUpdate(msg.jobID, msg.stdout, msg.stderr)
	}

	if msg.terminal {
		// The job hit a terminal state on the daemon side. The
		// tool.result event will arrive shortly with the final
		// captured output; nothing useful left to poll.
		return m, nil
	}

	return m, pollJobLogs(m.parent, m.client, msg.jobID)
}

func (m *model) onStreamDone() (tea.Model, tea.Cmd) {
	cmds := m.text.flush()
	cmds = append(cmds, m.tools.commit()...)

	m.finishTurn()

	return m, tea.Sequence(cmds...)
}

func (m *model) onStreamError(msg streamErrorMsg) (tea.Model, tea.Cmd) {
	cmds := m.text.flush()
	cmds = append(cmds, m.tools.commit()...)
	cmds = append(cmds, tea.Println("\n"+
		m.theme.errorDot.Render("●")+" "+
		m.theme.errText.Render("error: ")+msg.err.Error()))

	m.finishTurn()

	return m, tea.Sequence(cmds...)
}

func (m *model) finishTurn() {
	m.streaming = false
	m.streamCancel = nil
	m.tools.reset()
	m.text.endBlock()
	m.flash = ""
}
