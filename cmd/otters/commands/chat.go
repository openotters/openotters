package commands

import (
	"context"

	"github.com/google/uuid"
	"github.com/merlindorin/go-shared/pkg/cmd"

	"github.com/openotters/openotters/internal/chatui"
)

// Chat is the `otters chat <agent>` subcommand. The actual UI lives in
// internal/chatui; this file is only CLI glue.
type Chat struct {
	Ref       string `arg:"" name:"agent" help:"Agent ID or name"`
	SessionID string `short:"s" name:"session" help:"Reuse a named session (default: fresh ephemeral session per invocation)" default:""`
}

func (c *Chat) Run(ctx context.Context, _ *cmd.Commons, d *Daemon) error {
	rc, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	sessionID := c.SessionID
	if sessionID == "" {
		sessionID = "cli:chat:" + uuid.NewString()
	}

	return chatui.Run(ctx, chatui.Config{
		Ref:       c.Ref,
		SessionID: sessionID,
	}, rc)
}
