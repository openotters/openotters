package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/merlindorin/go-shared/pkg/cmd"
	daemonv2 "github.com/openotters/cli/api/v1"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

type Chat struct {
	Ref       string `arg:"" help:"Agent ID or name"`
	SessionID string `help:"Session ID" default:"cli:default"`
}

func (c *Chat) Run(ctx context.Context, _ *cmd.Commons, d *Daemon) error {
	rc, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	p := tea.NewProgram(newModel(ctx, c, rc), tea.WithAltScreen())
	_, err = p.Run()

	return err
}

type streamStartedMsg struct {
	stream daemonv2.Runtime_ChatStreamWithAgentClient
}

type streamEventMsg struct {
	event  *daemonv2.ChatStreamEvent
	stream daemonv2.Runtime_ChatStreamWithAgentClient
}

type streamDoneMsg struct{}

type streamErrorMsg struct {
	err error
}

type model struct {
	cfg    *Chat
	client daemonv2.RuntimeClient
	ctx    context.Context //nolint:containedctx // needed for gRPC calls within Bubble Tea

	input    textarea.Model
	viewport viewport.Model
	messages []string
	steps    []string
	waiting  bool
	width    int
	height   int
	renderer *glamour.TermRenderer

	userStyle      lipgloss.Style
	assistantStyle lipgloss.Style
	stepStyle      lipgloss.Style
	toolStyle      lipgloss.Style
	statusStyle    lipgloss.Style
}

func newModel(ctx context.Context, cfg *Chat, rc daemonv2.RuntimeClient) *model {
	ti := textarea.New()
	ti.Placeholder = "Type a message... (Enter to send, Ctrl+C to quit)"
	ti.Focus()
	ti.SetHeight(3)
	ti.ShowLineNumbers = false
	ti.KeyMap.InsertNewline.SetEnabled(false)

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)

	vp := viewport.New(0, 0)
	vp.KeyMap = viewport.KeyMap{}

	return &model{
		cfg:      cfg,
		client:   rc,
		ctx:      ctx,
		input:    ti,
		viewport: vp,
		renderer: renderer,
		userStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")).Bold(true),
		assistantStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).Bold(true),
		stepStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).Italic(true),
		toolStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")),
		statusStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")),
	}
}

func (m *model) Init() tea.Cmd {
	return textarea.Blink
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case streamStartedMsg:
		return m, listenStream(msg.stream)

	case streamEventMsg:
		return m.handleStreamEvent(msg.event, msg.stream)

	case streamDoneMsg:
		m.waiting = false
		m.updateViewport()

		return m, nil

	case streamErrorMsg:
		m.messages = append(m.messages,
			m.assistantStyle.Render("agent")+" "+
				m.statusStyle.Render("(error)")+"\n"+msg.err.Error(),
		)
		m.steps = nil
		m.waiting = false
		m.updateViewport()

		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(msg.Width)
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 6
		m.updateViewport()
	}

	var c tea.Cmd
	m.input, c = m.input.Update(msg)

	return m, c
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type { //nolint:exhaustive // only handling relevant keys
	case tea.KeyCtrlC, tea.KeyEsc:
		return m, tea.Quit

	case tea.KeyPgUp:
		m.viewport.HalfPageUp()

		return m, nil

	case tea.KeyPgDown:
		m.viewport.HalfPageDown()

		return m, nil

	case tea.KeyEnter:
		if m.waiting {
			return m, nil
		}

		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}

		m.messages = append(m.messages, m.userStyle.Render("you")+"\n"+text)
		m.steps = nil
		m.input.Reset()
		m.waiting = true
		m.updateViewport()

		return m, m.openStream(text)
	}

	var c tea.Cmd
	m.input, c = m.input.Update(msg)

	return m, c
}

func (m *model) openStream(prompt string) tea.Cmd {
	return func() tea.Msg {
		stream, err := m.client.ChatStreamWithAgent(m.ctx, &daemonv2.ChatStreamRequest{
			Ref:       m.cfg.Ref,
			SessionId: m.cfg.SessionID,
			Prompt:    prompt,
		})
		if err != nil {
			return streamErrorMsg{err: err}
		}

		return streamStartedMsg{stream: stream}
	}
}

func listenStream(stream daemonv2.Runtime_ChatStreamWithAgentClient) tea.Cmd {
	return func() tea.Msg {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return streamDoneMsg{}
		}

		if err != nil {
			return streamErrorMsg{err: err}
		}

		return streamEventMsg{event: event, stream: stream}
	}
}

func (m *model) handleStreamEvent(
	event *daemonv2.ChatStreamEvent, stream daemonv2.Runtime_ChatStreamWithAgentClient,
) (tea.Model, tea.Cmd) {
	switch event.Type {
	case "step.start":
		m.steps = append(m.steps, m.stepStyle.Render(
			fmt.Sprintf("  step %d...", event.Step),
		))
		m.updateViewportWithSteps()

	case "tool.call":
		input := truncate(event.Content, 80)
		m.steps = append(m.steps, m.toolStyle.Render(
			fmt.Sprintf("  -> %s(%s)", event.Tool, input),
		))
		m.updateViewportWithSteps()

	case "tool.result":
		result := truncate(event.Content, 120)
		m.steps = append(m.steps, m.stepStyle.Render(
			fmt.Sprintf("  <- %s: %s", event.Tool, result),
		))
		m.updateViewportWithSteps()

	case "text.delta":
		m.steps = append(m.steps, m.stepStyle.Render("  ...typing"))
		m.updateViewportWithSteps()

	case "message.create":
		rendered, err := m.renderer.Render(event.Content)
		if err != nil {
			rendered = event.Content
		}

		m.messages = append(m.messages,
			strings.Join(m.steps, "\n")+"\n"+
				m.assistantStyle.Render("agent")+"\n"+
				strings.TrimSpace(rendered),
		)
		m.steps = nil
		m.waiting = false
		m.updateViewport()

	case "error":
		m.messages = append(m.messages,
			m.assistantStyle.Render("agent")+" "+
				m.statusStyle.Render("(error)")+"\n"+event.Content,
		)
		m.steps = nil
		m.waiting = false
		m.updateViewport()
	}

	return m, listenStream(stream)
}

func (m *model) View() string {
	status := m.statusStyle.Render(
		fmt.Sprintf(" daemon | agent: %s | session: %s",
			m.cfg.Ref, m.cfg.SessionID),
	)

	if m.waiting {
		status += m.statusStyle.Render(" | thinking...")
	}

	return fmt.Sprintf("%s\n%s\n%s",
		m.viewport.View(),
		status,
		m.input.View(),
	)
}

func (m *model) updateViewport() {
	content := strings.Join(m.messages, "\n\n")
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

func (m *model) updateViewportWithSteps() {
	parts := make([]string, 0, len(m.messages)+1)
	parts = append(parts, m.messages...)

	if len(m.steps) > 0 {
		parts = append(parts, strings.Join(m.steps, "\n"))
	}

	m.viewport.SetContent(strings.Join(parts, "\n\n"))
	m.viewport.GotoBottom()
}

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}

	return s
}
