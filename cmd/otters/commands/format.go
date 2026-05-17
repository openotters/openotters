package commands

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// humanSize renders a byte count using binary (1024-based) units so
// that sub-KiB values keep their exact byte count and larger values
// collapse to a short label like "12.3 MiB".
func humanSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	suffix := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}[exp]

	return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), suffix)
}

// renderImageList is the shared rendering pipeline for `otters bin
// ls` and `otters image ls`. Both subcommands diff only by artifact
// type, the empty-list message, and the error-wrap prefix — the
// transport + table layout are identical. Extracted once so the
// dupl linter stays quiet and a future column (e.g. VERSION) lands
// in one place.
func renderImageList(
	ctx context.Context, common *cmd.Commons, d *Daemon,
	artifactType, emptyMessage, errPrefix string, quiet bool,
) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.ListImages(ctx, &daemonv1.ListImagesRequest{})
	if err != nil {
		return fmt.Errorf("%s: %w", errPrefix, unwrapRPC(err))
	}

	var images []*daemonv1.ImageInfo
	for _, img := range resp.GetImages() {
		if img.GetArtifactType() == artifactType {
			images = append(images, img)
		}
	}

	if quiet {
		for _, img := range images {
			fmt.Fprintln(os.Stdout, img.GetRef())
		}

		return nil
	}

	if len(images) == 0 {
		_, _ = common.Printer().Println(emptyMessage)

		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "REF\tDIGEST\tSIZE\tCREATED")

	for _, img := range images {
		digest := img.GetDigest()
		if len(digest) > 19 {
			digest = digest[:19]
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			img.GetRef(), digest, humanSize(img.GetSize()), formatCreated(img.GetCreatedAt()))
	}

	return w.Flush()
}
