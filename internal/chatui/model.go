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
		slash:     newSlashRegistry(th, func() string { return cfg.SessionID }),
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

	return strings.Join([]string{
		slot1,
		slot2,
		m.theme.renderRule(m.innerWidth()),
		m.theme.prompt.Render("›") + " " + m.input.View(),
		m.theme.renderRule(m.innerWidth()),
		m.renderStatusBar(),
	}, "\n")
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

		cmds = append(cmds, m.tools.onCall(
			msg.event.GetTool(),
			unwrapToolField(msg.event.GetContent(), "input"),
		))

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
