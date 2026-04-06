package commands

import (
	"context"

	"github.com/merlindorin/go-shared/pkg/cmd"
	daemonv1 "github.com/openotters/cli/api/v1"
	"go.uber.org/zap"
)

type Stop struct {
	Ref string `arg:"" help:"Agent ID or name"`
}

func (s *Stop) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err = c.StopAgent(ctx, &daemonv1.StopAgentRequest{Ref: s.Ref}); err != nil {
		return err
	}

	common.MustLogger().Info("agent stopped", zap.String("ref", s.Ref))

	return nil
}
