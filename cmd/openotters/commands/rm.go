package commands

import (
	"context"

	"github.com/merlindorin/go-shared/pkg/cmd"
	daemonv1 "github.com/openotters/cli/api/v1"
	"go.uber.org/zap"
)

type Rm struct {
	Ref   string `arg:"" help:"Agent ID or name"`
	Force bool   `short:"f" help:"Force remove (stop if running)" default:"false"`
}

func (r *Rm) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	if r.Force {
		_, _ = c.StopAgent(ctx, &daemonv1.StopAgentRequest{Ref: r.Ref})
	}

	if _, err = c.RemoveAgent(ctx, &daemonv1.RemoveAgentRequest{Ref: r.Ref}); err != nil {
		return err
	}

	common.MustLogger().Info("agent removed", zap.String("ref", r.Ref))

	return nil
}
