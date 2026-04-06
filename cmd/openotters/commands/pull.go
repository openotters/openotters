package commands

import (
	"context"
	"fmt"

	"github.com/merlindorin/go-shared/pkg/cmd"
	daemonv1 "github.com/openotters/cli/api/v1"
	"go.uber.org/zap"
)

type Pull struct {
	Ref  string   `arg:"" help:"Image reference to pull (e.g. ghcr.io/openotters/agents/meteo:1.0.0)"`
	Tags []string `short:"t" help:"Local tags (auto-generated if empty)" optional:""`
}

func (p *Pull) Run(_ context.Context, common *cmd.Commons, d *Daemon) error {
	logger := common.MustLogger()

	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.PullAgentImage(context.Background(), &daemonv1.PullRequest{
		Ref:  p.Ref,
		Tags: p.Tags,
	})
	if err != nil {
		return fmt.Errorf("pull failed: %w", err)
	}

	logger.Info("pulled",
		zap.String("digest", resp.Digest),
		zap.Strings("tags", resp.Tags),
	)

	return nil
}
