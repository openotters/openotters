// Package chatui runs the `otters chat <agent>` interactive UI.
//
// The entry point is Run. Everything else in the package is unexported;
// the UI is a single self-contained composition of small components:
//
//	theme.go   — lipgloss palette + rule / status-bar renderers
//	tools.go   — toolBlock: spinner → ✓ "freezes in place" → commit
//	text.go    — textBlock: line-by-line commit of streamed deltas
//	slash.go   — slashRegistry: /quit, /help, /clear, /session
//	stream.go  — gRPC stream message types + openStream/recvNext
//	model.go   — bubbletea model composing all of the above
package chatui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// Config is the runtime configuration for one chat session.
type Config struct {
	Ref       string // agent id or name
	SessionID string // persistent session key
}

// Run starts a blocking bubbletea chat session against client. Exits
// cleanly on /quit, Ctrl+C at the prompt, or stdin EOF.
func Run(ctx context.Context, cfg Config, client daemonv1.RuntimeClient) error {
	m := newModel(ctx, cfg, client)

	_, err := tea.NewProgram(m).Run()

	return err
}
