package commands

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/merlindorin/go-shared/pkg/cmd"
	daemonv1 "github.com/openotters/cli/api/v1"
	"go.uber.org/zap"
)

type ImageLs struct{}

func (i *ImageLs) Run(
	_ context.Context, common *cmd.Commons, d *Daemon,
) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.ListImages(context.Background(), &daemonv1.ListImagesRequest{})
	if err != nil {
		return fmt.Errorf("listing images: %w", err)
	}

	if len(resp.Images) == 0 {
		common.MustLogger().Info("no images")

		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "REF\tDIGEST\tTYPE\tSIZE")

	for _, img := range resp.Images {
		digest := img.Digest
		if len(digest) > 19 {
			digest = digest[:19]
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%d\n",
			img.Ref, digest, img.ArtifactType, img.Size)
	}

	return w.Flush()
}

type ImageRm struct {
	Ref string `arg:"" help:"Image reference (e.g. meteo:v1.0)"`
}

func (i *ImageRm) Run(
	_ context.Context, common *cmd.Commons, d *Daemon,
) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = c.RemoveImage(context.Background(), &daemonv1.RemoveImageRequest{
		Ref: i.Ref,
	})
	if err != nil {
		return fmt.Errorf("removing image: %w", err)
	}

	common.MustLogger().Info("image removed", zap.String("ref", i.Ref))

	return nil
}

type ImageDescribe struct {
	Ref string `arg:"" help:"Image reference (e.g. meteo:v1.0)"`
}

func (i *ImageDescribe) Run(
	_ context.Context, common *cmd.Commons, d *Daemon,
) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.DescribeImage(context.Background(), &daemonv1.DescribeImageRequest{
		Ref: i.Ref,
	})
	if err != nil {
		return fmt.Errorf("describing image: %w", err)
	}

	logger := common.MustLogger()
	logger.Info("image",
		zap.String("ref", resp.Ref),
		zap.String("digest", resp.Digest),
		zap.String("type", resp.ArtifactType),
	)

	if len(resp.Labels) > 0 {
		logger.Info("labels", zap.Any("labels", resp.Labels))
	}

	logger.Info("config", zap.String("json", resp.Config))

	for _, l := range resp.Layers {
		logger.Info("layer", zap.String("info", l))
	}

	return nil
}
