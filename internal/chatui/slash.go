package chatui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// slashCommand is one entry in the slashRegistry.
type slashCommand struct {
	names []string
	help  string
	run   func() (tea.Cmd, bool) // cmd, quit
}

// slashRegistry owns the `/foo` commands available at the chat prompt.
type slashRegistry struct {
	theme   *theme
	session func() string
	cmds    []slashCommand
}

func newSlashRegistry(t *theme, session func() string) *slashRegistry {
	r := &slashRegistry{theme: t, session: session}

	r.cmds = []slashCommand{
		{
			names: []string{"/quit", "/q", "/exit"},
			help:  "leave the chat",
			run:   func() (tea.Cmd, bool) { return nil, true },
		},
		{
			names: []string{"/help", "/?"},
			help:  "show this list",
			run:   func() (tea.Cmd, bool) { return r.Help(), false },
		},
		{
			names: []string{"/clear"},
			help:  "clear the screen",
			run:   func() (tea.Cmd, bool) { return tea.ClearScreen, false },
		},
		{
			names: []string{"/session"},
			help:  "show the current session id",
			run: func() (tea.Cmd, bool) {
				return tea.Println("\n" +
					r.theme.infoDot.Render("●") + " " +
					r.theme.dim.Render("session: "+r.session())), false
			},
		},
	}

	return r
}

// Dispatch executes the slash command matching line.
// Returns (cmd, quit).
func (r *slashRegistry) Dispatch(line string) (tea.Cmd, bool) {
	head, _, _ := strings.Cut(line, " ")

	for _, c := range r.cmds {
		for _, n := range c.names {
			if n == head {
				return c.run()
			}
		}
	}

	return tea.Println("\n" +
		r.theme.errorDot.Render("●") + " " +
		r.theme.errText.Render("unknown command: ") + head), false
}

// Help returns a tea.Cmd that prints the command list.
func (r *slashRegistry) Help() tea.Cmd {
	return tea.Println("\n" + r.renderHelp())
}

func (r *slashRegistry) renderHelp() string {
	var b strings.Builder

	b.WriteString(r.theme.infoDot.Render("●"))
	b.WriteString(" ")
	b.WriteString(r.theme.dim.Render("commands"))
	b.WriteString("\n")

	for _, c := range r.cmds {
		b.WriteString("    ")
		b.WriteString(r.theme.toolName.Render(strings.Join(c.names, ", ")))
		b.WriteString("  ")
		b.WriteString(r.theme.dim.Render(c.help))
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}
