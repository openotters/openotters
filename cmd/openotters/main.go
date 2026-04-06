package main

import (
	"context"

	"github.com/alecthomas/kong"
	c "github.com/merlindorin/go-shared/pkg/cmd"

	"github.com/openotters/cli/cmd/openotters/commands"
)

const (
	name        = "openotters"
	description = "openotters CLI — build, push, and run AI agents"
)

//nolint:gochecknoglobals // set by ldflags at build time
var (
	version     = "dev"
	commit      = "dirty"
	date        = "latest"
	buildSource = "source"
)

func main() {
	cli := CMD{
		Commons: &c.Commons{Version: c.NewVersion(name, version, commit, buildSource, date)},
		Daemon:  commands.NewDaemon(),

		Build: &commands.Build{},
		Pull:  &commands.Pull{},
		Push:  &commands.Push{},
		Run:   &commands.Run{},
		Ps:    &commands.Ps{},
		Stop:  &commands.Stop{},
		Rm:    &commands.Rm{},
		Chat:  &commands.Chat{},
	}

	ctx := kong.Parse(
		&cli,
		kong.Name(name),
		kong.Description(description),
		kong.UsageOnError(),
		kong.DefaultEnvars("OPENOTTERS"),
	)

	ctx.BindTo(context.Background(), (*context.Context)(nil))
	ctx.FatalIfErrorf(ctx.Run(cli.Commons, cli.Daemon))
}

type CMD struct {
	*c.Commons
	*commands.Daemon `embed:""`

	Build *commands.Build `cmd:"" help:"Build an agent OCI artifact from an Agentfile"`
	Pull  *commands.Pull  `cmd:"" help:"Pull an agent image from a remote registry"`
	Push  *commands.Push  `cmd:"" help:"Push a local image to a remote registry"`
	Run   *commands.Run   `cmd:"" help:"Run an agent from an Agentfile or registry reference"`
	Ps    *commands.Ps    `cmd:"" help:"List running agents"`
	Stop  *commands.Stop  `cmd:"" help:"Stop a running agent"`
	Rm    *commands.Rm    `cmd:"" help:"Remove an agent"`
	Chat  *commands.Chat  `cmd:"" help:"Start interactive chat with an agent"`
	Image ImageCmd        `cmd:"" help:"Manage images in the local registry"`
}

type ImageCmd struct {
	Ls       *commands.ImageLs       `cmd:"" help:"List images in the local registry"`
	Rm       *commands.ImageRm       `cmd:"" help:"Remove an image from the local registry"`
	Describe *commands.ImageDescribe `cmd:"" help:"Describe an image in the local registry"`
}
