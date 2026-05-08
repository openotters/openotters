package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/merlindorin/go-shared/pkg/cmd"

	"github.com/openotters/agentfile/spec"
	daemonv1 "github.com/openotters/openotters/api/v1"
)

// BinBuild builds a multi-arch tool OCI image on the daemon, then
// pushes it to the local embedded registry under the requested tags.
// Platforms are specified as `<os>/<arch>:<abs-path>` — paths are
// resolved on the *daemon host's* filesystem, same convention as
// `otters build` for agents.
//
// This same mechanism is used to package *runtimes* (the otters agent
// runtime binary, or any other long-running host) since a runtime is,
// from the registry's point of view, just another static binary
// delivered as a multi-arch bin-tool image. Agents consume tool
// images via `BIN` directives; the runtime image is pulled by the
// daemon when provisioning an agent host.
type BinBuild struct {
	Name        string   `short:"n" help:"Tool name (required)" required:""`
	Description string   `short:"d" help:"One-line description" default:""`
	Usage       string   `short:"u" help:"Usage guidelines (markdown) baked into the image as USAGE.md" default:""`
	Source      string   `short:"s" help:"Upstream repo URL — stamped as org.opencontainers.image.source so ghcr.io auto-links the package and inherits its visibility" default:""`
	Tags        []string `short:"t" help:"Local tags (default: <name>:latest)" optional:""`
	Platforms   []string `arg:"" name:"platform" help:"One or more <os>/<arch>:<bin-path> entries (e.g. linux/amd64:/tmp/jq-linux-amd64)"`
}

func (b *BinBuild) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	if len(b.Platforms) == 0 {
		return errors.New("at least one platform is required (e.g. linux/amd64:/tmp/jq-linux-amd64)")
	}

	platforms := make([]*daemonv1.ToolPlatform, 0, len(b.Platforms))

	for _, p := range b.Platforms {
		osArch, path, ok := strings.Cut(p, ":")
		if !ok {
			return fmt.Errorf("invalid platform %q, expected os/arch:path", p)
		}

		goos, goarch, ok := strings.Cut(osArch, "/")
		if !ok {
			return fmt.Errorf("invalid platform %q, expected os/arch", osArch)
		}

		abs, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolving %s: %w", path, err)
		}

		platforms = append(platforms, &daemonv1.ToolPlatform{
			Os:      goos,
			Arch:    goarch,
			BinPath: abs,
		})
	}

	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.BuildToolImage(ctx, &daemonv1.BuildToolImageRequest{
		Name:        b.Name,
		Description: b.Description,
		Usage:       b.Usage,
		Source:      b.Source,
		Tags:        b.Tags,
		Platforms:   platforms,
	})
	if err != nil {
		return fmt.Errorf("building: %w", unwrapRPC(err))
	}

	p := common.Printer()
	_, _ = p.Printf("built %s (%d platforms)\n", resp.GetRef(), len(platforms))
	_, _ = p.Printf("  digest: %s\n", resp.GetDigest())
	_, _ = p.Printf("  tags:   %s\n", strings.Join(resp.GetTags(), ", "))

	return nil
}

// BinPull pulls a tool image from a remote registry into the
// daemon's local registry. Thin wrapper over the shared PullAgentImage
// RPC — artifact kind is irrelevant to the pull mechanic.
type BinPull struct {
	Ref  string   `arg:"" name:"ref" help:"Image reference to pull (e.g. ghcr.io/openotters/tools/jq:latest)"`
	Tags []string `short:"t" help:"Local tags (auto-generated if empty)" optional:""`
}

func (t *BinPull) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.PullAgentImage(ctx, &daemonv1.PullRequest{
		Ref:  t.Ref,
		Tags: t.Tags,
	})
	if err != nil {
		return fmt.Errorf("pull failed: %w", unwrapRPC(err))
	}

	p := common.Printer()
	_, _ = p.Printf("pulled %s\n", t.Ref)
	_, _ = p.Printf("  digest: %s\n", resp.GetDigest())
	_, _ = p.Printf("  tags:   %s\n", strings.Join(resp.GetTags(), ", "))

	return nil
}

// BinPush pushes a local tool image to a remote registry. Same
// codepath as PushAgentImage — generic image push.
type BinPush struct {
	Ref string `arg:"" name:"ref" help:"Image reference to push (e.g. ghcr.io/openotters/tools/jq:0.1)"`
}

func (t *BinPush) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.PushAgentImage(ctx, &daemonv1.PushRequest{Ref: t.Ref})
	if err != nil {
		return fmt.Errorf("push failed: %w", unwrapRPC(err))
	}

	p := common.Printer()
	_, _ = p.Printf("pushed %s\n", resp.GetRef())
	_, _ = p.Printf("  digest: %s\n", resp.GetDigest())

	return nil
}

// BinLs lists binary images in the daemon's local registry. Uses the
// same ListImages RPC as `otters image ls` but filters the result to
// images whose artifactType matches spec.BinArtifactType.
type BinLs struct {
	Quiet bool `short:"q" help:"Only display image refs (useful for piping)" default:"false"`
}

func (t *BinLs) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.ListImages(ctx, &daemonv1.ListImagesRequest{})
	if err != nil {
		return fmt.Errorf("listing: %w", unwrapRPC(err))
	}

	var bins []*daemonv1.ImageInfo

	for _, img := range resp.GetImages() {
		if img.GetArtifactType() == spec.BinArtifactType {
			bins = append(bins, img)
		}
	}

	if t.Quiet {
		for _, img := range bins {
			fmt.Fprintln(os.Stdout, img.GetRef())
		}

		return nil
	}

	if len(bins) == 0 {
		_, _ = common.Printer().Println("no binary images")

		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "REF\tDIGEST\tSIZE")

	for _, img := range bins {
		digest := img.GetDigest()
		if len(digest) > 19 {
			digest = digest[:19]
		}

		fmt.Fprintf(w, "%s\t%s\t%s\n", img.GetRef(), digest, humanSize(img.GetSize()))
	}

	return w.Flush()
}

// BinRm removes one or more binary images from the daemon's local registry.
type BinRm struct {
	Refs []string `arg:"" name:"ref" help:"Binary image reference (one or more, e.g. jq:latest)"`
}

func (t *BinRm) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	if len(t.Refs) == 0 {
		return errors.New("at least one image is required")
	}

	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	p := common.Printer()

	var errs []error

	for _, ref := range t.Refs {
		if _, rmErr := c.RemoveImage(ctx, &daemonv1.RemoveImageRequest{Ref: ref}); rmErr != nil {
			clean := unwrapRPC(rmErr)
			fmt.Fprintf(os.Stderr, "rm %s: %v\n", ref, clean)
			errs = append(errs, clean)

			continue
		}

		_, _ = p.Printf("removed %s\n", ref)
	}

	return errors.Join(errs...)
}

// BinInspect shows the full manifest of a binary image in the local
// registry. Same underlying RPC as `otters image inspect` — the
// separate command exists for CLI symmetry, not for filtering.
type BinInspect struct {
	Ref string `arg:"" name:"ref" help:"Binary image reference"`
}

func (b *BinInspect) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.DescribeImage(ctx, &daemonv1.DescribeImageRequest{Ref: b.Ref})
	if err != nil {
		return fmt.Errorf("inspecting image: %w", unwrapRPC(err))
	}

	p := common.Printer()
	_, _ = p.Printf("ref:    %s\n", resp.GetRef())
	_, _ = p.Printf("digest: %s\n", resp.GetDigest())
	_, _ = p.Printf("type:   %s\n", resp.GetArtifactType())

	if len(resp.Labels) > 0 {
		_, _ = p.Println("labels:")

		for k, v := range resp.Labels {
			_, _ = p.Printf("  %s: %s\n", k, v)
		}
	}

	if len(resp.Layers) > 0 {
		_, _ = p.Println("layers:")

		for _, l := range resp.Layers {
			_, _ = p.Printf("  %s\n", l)
		}
	}

	return nil
}
