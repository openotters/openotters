package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kong"
	c "github.com/merlindorin/go-shared/pkg/cmd"

	"github.com/openotters/openotters/cmd/ottersd/commands"
)

const (
	name        = "ottersd"
	description = "otters daemon — manages AI agent runtimes"
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
		Commons: &c.Commons{
			Version: c.NewVersion(name, version, commit, buildSource, date),
		},
		SQLite: c.NewSQLite(),
		Config: c.NewConfig(),

		Serve: &commands.Serve{},
	}

	ctx := kong.Parse(
		&cli,
		kong.Name(name),
		kong.Description(description),
		kong.UsageOnError(),
		kong.DefaultEnvars("OTTERS"),
	)

	// Signal-wired root context: SIGINT / SIGTERM cancel the tree so
	// the daemon's pool, gRPC server, and state writes drain cleanly.
	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctx.BindTo(runCtx, (*context.Context)(nil))
	ctx.FatalIfErrorf(ctx.Run(cli.Commons, cli.SQLite))
}

type CMD struct {
	*c.Commons
	*c.SQLite `embed:""`
	*c.Config `embed:""`

	Serve *commands.Serve `cmd:"" default:"1" help:"Start the daemon"`
}
