package commands

import (
	"context"
	"fmt"

	"github.com/merlindorin/go-shared/pkg/cmd"
	daemonv1 "github.com/openotters/cli/api/v1"
	"go.uber.org/zap"

	"github.com/openotters/agentfile/build"
	"github.com/openotters/agentfile/export"
)

type Build struct {
	File string   `short:"f" help:"Path to Agentfile" default:"Agentfile"`
	Tags []string `short:"t" help:"Tag the image (e.g. meteo:v1.0)" optional:""`
}

func (b *Build) Run(_ context.Context, common *cmd.Commons, d *Daemon) error {
	logger := common.MustLogger()

	af, store, digest, err := build.FromFile(context.Background(), b.File)
	if err != nil {
		return fmt.Errorf("building: %w", err)
	}

	ociData, err := export.Export(store)
	if err != nil {
		return fmt.Errorf("exporting artifact: %w", err)
	}

	logger.Info("built locally",
		zap.String("digest", digest.String()),
		zap.Int("size", len(ociData)),
	)

	tags := b.Tags
	if len(tags) == 0 {
		name := af.Agent.Name
		if name == "" {
			name = "agent"
		}

		tags = []string{name + ":latest"}
	}

	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.SaveAgentImage(context.Background(), &daemonv1.SaveAgentImageRequest{
		OciArtifact: ociData,
		Tags:        tags,
	})
	if err != nil {
		return fmt.Errorf("saving to daemon: %w", err)
	}

	logger.Info("saved to daemon registry",
		zap.String("digest", resp.Digest),
		zap.Strings("tags", resp.Tags),
	)

	return nil
}
