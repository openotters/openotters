package commands

import (
	"context"
	"fmt"

	"github.com/merlindorin/go-shared/pkg/cmd"
	daemonv1 "github.com/openotters/cli/api/v1"
	"go.uber.org/zap"
)

type Push struct {
	Ref string `arg:"" help:"Image reference to push (e.g. ghcr.io/org/agent:v1.0)"`
}

func (p *Push) Run(_ context.Context, common *cmd.Commons, d *Daemon) error {
	logger := common.MustLogger()

	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.PushAgentImage(context.Background(), &daemonv1.PushRequest{
		Ref: p.Ref,
	})
	if err != nil {
		return fmt.Errorf("push failed: %w", err)
	}

	logger.Info("pushed",
		zap.String("ref", resp.Ref),
		zap.String("digest", resp.Digest),
	)

	return nil
}
