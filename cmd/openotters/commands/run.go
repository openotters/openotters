package commands

import (
	"context"
	"fmt"

	"github.com/merlindorin/go-shared/pkg/cmd"
	daemonv1 "github.com/openotters/cli/api/v1"
	"go.uber.org/zap"
)

type Run struct {
	Ref  string `arg:"" help:"Image ref to run (e.g. meteo:v1.0 or ghcr.io/org/agent:v1.0)"`
	Name string `help:"Instance name (auto-generated if empty)" optional:""`
}

func (r *Run) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	logger := common.MustLogger()

	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.CreateAgent(ctx, &daemonv1.CreateAgentRequest{
		Name: r.Name,
		Ref:  r.Ref,
	})
	if err != nil {
		return fmt.Errorf("creating agent: %w", err)
	}

	logger.Info("agent created",
		zap.String("id", resp.Id),
		zap.String("name", resp.Name),
		zap.String("status", resp.Status),
	)

	return nil
}
