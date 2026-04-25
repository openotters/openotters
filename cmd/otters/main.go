package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kong"
	c "github.com/merlindorin/go-shared/pkg/cmd"

	"github.com/openotters/openotters/cmd/otters/commands"
)

const (
	name        = "otters"
	description = "otters CLI — build, push, and run AI agents"
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

		// Top-level lifecycle shortcuts — `otters run`, `otters ps`, etc.
		Run:    &commands.Run{},
		Ps:     &commands.Ps{},
		Start:  &commands.Start{},
		Stop:   &commands.Stop{},
		Rm:     &commands.Rm{},
		Chat:   &commands.Chat{},
		Prompt: &commands.Prompt{},
		Logs:   &commands.Logs{},
		Info:   &commands.Info{},

		Models: ModelsCmd{Ls: &commands.ModelsLs{}},

		// `otters agent …` — fully-qualified form, same commands.
		Agent: AgentCmd{
			Run:     &commands.Run{},
			Ls:      &commands.Ps{},
			Start:   &commands.Start{},
			Stop:    &commands.Stop{},
			Rm:      &commands.Rm{},
			Chat:    &commands.Chat{},
			Prompt:  &commands.Prompt{},
			Logs:    &commands.Logs{},
			Inspect: &commands.AgentInspect{},
		},
	}

	ctx := kong.Parse(
		&cli,
		kong.Name(name),
		kong.Description(description),
		kong.UsageOnError(),
		kong.DefaultEnvars("OTTERS"),
		kong.ExplicitGroups(dockerStyleGroups),
		kong.Help(dockerStyleHelp),
		kong.Vars{"version": cli.Version.String()},
	)

	// Signal-wired root context: Ctrl-C during `chat`, `run`, or a
	// long streaming `prompt` cancels the gRPC call instead of
	// terminating mid-stream with a bare interrupt.
	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctx.BindTo(runCtx, (*context.Context)(nil))
	ctx.FatalIfErrorf(ctx.Run(cli.Commons, cli.Daemon))
}

type CMD struct {
	*c.Commons
	*commands.Daemon `embed:""`

	ShowVersion kong.VersionFlag `name:"version" help:"Show version information and exit."`

	// Running-agent lifecycle, top-level shortcuts.
	Run    *commands.Run    `cmd:"" group:"lifecycle" help:"Run an agent from an Agentfile, build context, or registry reference"`
	Ps     *commands.Ps     `cmd:"" group:"lifecycle" aliases:"ls" help:"List running agents"`
	Start  *commands.Start  `cmd:"" group:"lifecycle" help:"Start a stopped agent"`
	Stop   *commands.Stop   `cmd:"" group:"lifecycle" help:"Stop a running agent"`
	Rm     *commands.Rm     `cmd:"" group:"lifecycle" help:"Remove a running agent"`
	Chat   *commands.Chat   `cmd:"" group:"lifecycle" help:"Start interactive chat with an agent"`
	Prompt *commands.Prompt `cmd:"" group:"lifecycle" help:"Send a single prompt to an agent and print the response (non-interactive)"`
	Logs   *commands.Logs   `cmd:"" group:"lifecycle" help:"Print the runtime log file for an agent"`
	Info   *commands.Info   `cmd:"" help:"Show daemon info (sockets, paths, version, agent counts)"`

	// Management groups.
	Agent  AgentCmd  `cmd:"" group:"management" help:"Manage running agents — run, ps, start, stop, rm, chat, prompt, logs, inspect."`
	Image  ImageCmd  `cmd:"" group:"management" help:"Manage agent images — build, push, pull, list, remove, inspect."`
	Bin    BinCmd    `cmd:"" group:"management" help:"Manage binary tools — build, push, pull, list, remove, inspect."`
	Models ModelsCmd `cmd:"" group:"management" help:"List models available from configured providers."`
}

// AgentCmd is the fully-qualified form of the running-agent lifecycle
// commands. The top-level fields on CMD mirror these, so `otters run`
// and `otters agent run` are interchangeable — the top-level form is
// shorter, the sub-command form groups discoverably under `--help`.
type AgentCmd struct {
	Run     *commands.Run          `cmd:"" help:"Run an agent from an Agentfile or registry reference"`
	Ls      *commands.Ps           `cmd:"" aliases:"ps" help:"List running agents"`
	Start   *commands.Start        `cmd:"" help:"Start a stopped agent"`
	Stop    *commands.Stop         `cmd:"" help:"Stop a running agent"`
	Rm      *commands.Rm           `cmd:"" help:"Remove a running agent"`
	Chat    *commands.Chat         `cmd:"" help:"Start interactive chat with an agent"`
	Prompt  *commands.Prompt       `cmd:"" help:"Send a single prompt to an agent and print the response (non-interactive)"`
	Logs    *commands.Logs         `cmd:"" help:"Print the runtime log file for an agent"`
	Inspect *commands.AgentInspect `cmd:"" aliases:"desc,describe" help:"Show detailed information for a running agent"`
}

type ImageCmd struct {
	Build   *commands.ImageBuild   `cmd:"" help:"Build an agent OCI image from an Agentfile"`
	Pull    *commands.ImagePull    `cmd:"" help:"Pull an agent image from a remote registry"`
	Push    *commands.ImagePush    `cmd:"" help:"Push a local agent image to a remote registry"`
	Ls      *commands.ImageLs      `cmd:"" aliases:"list" help:"List agent images in the local registry"`
	Rm      *commands.ImageRm      `cmd:"" aliases:"remove" help:"Remove an agent image from the local registry"`
	Inspect *commands.ImageInspect `cmd:"" aliases:"desc,describe" help:"Show detailed information for an agent image"`
}

type BinCmd struct {
	Build   *commands.BinBuild   `cmd:"" help:"Build a multi-arch binary image from local binaries"`
	Pull    *commands.BinPull    `cmd:"" help:"Pull a binary image from a remote registry"`
	Push    *commands.BinPush    `cmd:"" help:"Push a local binary image to a remote registry"`
	Ls      *commands.BinLs      `cmd:"" aliases:"list" help:"List binary images in the local registry"`
	Rm      *commands.BinRm      `cmd:"" aliases:"remove" help:"Remove a binary image from the local registry"`
	Inspect *commands.BinInspect `cmd:"" aliases:"desc,describe" help:"Show detailed information for a binary image"`
}

type ModelsCmd struct {
	Ls *commands.ModelsLs `cmd:"" aliases:"list" help:"List models reachable via configured providers"`
}
