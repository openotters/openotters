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
		// Config lives at ~/.otters/ottersd.yaml (and
		// /etc/otters/ottersd.yaml host-scoped) so the daemon's
		// config sits alongside the rest of its state under
		// ~/.otters/ — providers.yaml, daemon.db, agents/, logs/,
		// registry/. WithGroup("otters") is what produces that
		// shape; without it cmd.NewConfig() would default to
		// ~/.ottersd/config.yaml (file split off from data).
		Config: c.NewConfig(c.WithGroup("otters")),

		Serve: &commands.Serve{},
	}

	ctx := kong.Parse(
		&cli,
		kong.Name(name),
		kong.Description(description),
		kong.UsageOnError(),
		kong.DefaultEnvars("OTTERS"),
		kong.Vars{"version": cli.Version.String()},
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

	ShowVersion kong.VersionFlag `name:"version" help:"Show version information and exit."`

	Serve *commands.Serve `cmd:"" default:"1" help:"Start the daemon"`
}
