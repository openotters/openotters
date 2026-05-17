package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/merlindorin/go-shared/pkg/cmd"

	"github.com/openotters/agentfile/spec"
	daemonv1 "github.com/openotters/openotters/api/v1"
)

// ImageBuild builds an agent OCI artifact from an Agentfile. Thin
// client — the daemon does parsing, FROM resolution, OCI packing, and
// pushes to the embedded registry. Follows Docker's `docker build
// [OPTIONS] PATH` shape: PATH is the build context directory (defaults
// to the current directory); -f names the Agentfile within that
// context (defaults to <PATH>/Agentfile).
//
// Stdin shortcut: passing PATH="-" or -f "-" reads the Agentfile
// from stdin, same convention Docker uses. Useful for pipelining:
//
//	cat Agentfile | otters image build - -t foo:latest
//	otters image build -f - -t foo:latest <<EOF
//	FROM scratch
//	...
//	EOF
type ImageBuild struct {
	Path string   `arg:"" default:"." help:"Build context directory, or '-' to read the Agentfile from stdin"`
	File string   `short:"f" help:"Name of the Agentfile (defaults to <PATH>/Agentfile; '-' for stdin)" default:""`
	Tags []string `short:"t" help:"Tag the image (e.g. meteo:v1.0)" optional:""`
}

func (b *ImageBuild) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	abs, cleanup, err := resolveBuildSource(b.Path, b.File)
	if err != nil {
		return err
	}
	defer cleanup()

	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.BuildAgent(ctx, &daemonv1.BuildAgentRequest{
		AgentfilePath: abs,
		Tags:          b.Tags,
	})
	if err != nil {
		return fmt.Errorf("building: %w", unwrapRPC(err))
	}

	p := common.Printer()
	_, _ = p.Printf("built %s\n", resp.GetRef())
	_, _ = p.Printf("  digest: %s\n", resp.GetDigest())
	_, _ = p.Printf("  tags:   %s\n", strings.Join(resp.GetTags(), ", "))

	return nil
}

// resolveBuildSource picks the Agentfile to build. Extends
// resolveAgentfile with Docker-style stdin support: a literal "-"
// in either PATH or -f slurps stdin into a temporary file and
// returns its path plus a cleanup closure the caller must defer.
// Non-stdin callers receive a no-op cleanup so the defer pattern
// is uniform.
func resolveBuildSource(path, file string) (string, func(), error) {
	noop := func() {}

	if path == "-" || file == "-" {
		tmp, err := os.CreateTemp("", "Agentfile.*.stdin")
		if err != nil {
			return "", noop, fmt.Errorf("creating temp Agentfile: %w", err)
		}

		cleanup := func() {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}

		if _, copyErr := io.Copy(tmp, os.Stdin); copyErr != nil {
			cleanup()

			return "", noop, fmt.Errorf("reading stdin: %w", copyErr)
		}

		if closeErr := tmp.Close(); closeErr != nil {
			_ = os.Remove(tmp.Name())

			return "", noop, fmt.Errorf("closing temp Agentfile: %w", closeErr)
		}

		return tmp.Name(), func() { _ = os.Remove(tmp.Name()) }, nil
	}

	abs, err := resolveAgentfile(path, file)

	return abs, noop, err
}

// resolveAgentfile picks the Agentfile to build given Docker-style
// PATH + optional -f FILE. Accepts four shapes:
//   - PATH is a file: use that file, ignore FILE.
//   - PATH is a dir, FILE empty: <PATH>/Agentfile.
//   - PATH is a dir, FILE absolute: FILE.
//   - PATH is a dir, FILE relative: <PATH>/FILE.
func resolveAgentfile(path, file string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}

	var candidate string

	switch {
	case !info.IsDir():
		candidate = path
	case file == "":
		candidate = filepath.Join(path, "Agentfile")
	case filepath.IsAbs(file):
		candidate = file
	default:
		candidate = filepath.Join(path, file)
	}

	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", candidate, err)
	}

	if _, statErr := os.Stat(abs); statErr != nil {
		return "", fmt.Errorf("agentfile %s: %w", abs, statErr)
	}

	return abs, nil
}

// ImagePull pulls an agent image from a remote registry into the
// daemon's local registry.
type ImagePull struct {
	Ref  string   `arg:"" name:"ref" help:"Image reference to pull (e.g. ghcr.io/openotters/agents/meteo:1.0.0)"`
	Tags []string `short:"t" help:"Local tags (auto-generated if empty)" optional:""`
}

func (p *ImagePull) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.PullAgentImage(ctx, &daemonv1.PullRequest{Ref: p.Ref, Tags: p.Tags})
	if err != nil {
		return fmt.Errorf("pull failed: %w", unwrapRPC(err))
	}

	out := common.Printer()
	_, _ = out.Printf("pulled %s\n", p.Ref)
	_, _ = out.Printf("  digest: %s\n", resp.GetDigest())
	_, _ = out.Printf("  tags:   %s\n", strings.Join(resp.GetTags(), ", "))

	return nil
}

// ImagePush pushes a local agent image to a remote registry.
type ImagePush struct {
	Ref string `arg:"" name:"ref" help:"Image reference to push (e.g. ghcr.io/org/agent:v1.0)"`
}

func (p *ImagePush) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.PushAgentImage(ctx, &daemonv1.PushRequest{Ref: p.Ref})
	if err != nil {
		return fmt.Errorf("push failed: %w", unwrapRPC(err))
	}

	out := common.Printer()
	_, _ = out.Printf("pushed %s\n", resp.GetRef())
	_, _ = out.Printf("  digest: %s\n", resp.GetDigest())

	return nil
}

// ImageLs lists only agent-artifact images in the local registry —
// bin and foreign-artifact rows are filtered out.
type ImageLs struct {
	Quiet bool `short:"q" help:"Only display image refs (useful for piping)" default:"false"`
}

func (a *ImageLs) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	return renderImageList(ctx, common, d,
		spec.AgentArtifactType, "no agent images", "listing agents", a.Quiet)
}

// formatCreated renders a unix-seconds timestamp as a human-readable
// "5 minutes ago" / "2 days ago" string. Returns "-" for zero (which
// the daemon emits when the manifest mtime can't be read).
func formatCreated(unixSec int64) string {
	if unixSec == 0 {
		return "-"
	}

	d := time.Since(time.Unix(unixSec, 0))

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

// ImageRm removes one or more agent images from the local registry.
type ImageRm struct {
	Refs []string `arg:"" name:"ref" help:"Image reference (one or more, e.g. meteo:v1.0)"`
}

func (a *ImageRm) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	if len(a.Refs) == 0 {
		return errors.New("at least one image is required")
	}

	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	p := common.Printer()

	var errs []error

	for _, ref := range a.Refs {
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

// ImageInspect shows the full manifest (config + layers + labels) of
// an agent image in the local registry.
type ImageInspect struct {
	Ref string `arg:"" name:"ref" help:"Image reference (e.g. meteo:v1.0)"`
}

func (a *ImageInspect) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	resp, err := c.DescribeImage(ctx, &daemonv1.DescribeImageRequest{Ref: a.Ref})
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

	if resp.Config != "" {
		_, _ = p.Println("config:")
		_, _ = p.Printf("  %s\n", resp.Config)
	}

	return nil
}
