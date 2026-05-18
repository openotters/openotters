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

		Link:   &commands.Link{},
		Unlink: &commands.Unlink{},
		Links:  &commands.Links{},

		Provider: ProviderCmd{
			Add:    &commands.ProviderAdd{},
			Edit:   &commands.ProviderEdit{},
			Rm:     &commands.ProviderRm{},
			Ls:     &commands.ProviderLs{},
			Models: &commands.ModelsLs{},
		},

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

	// Agent-to-agent linking. Top-level for discoverability;
	// directional A → B (one row per direction).
	Link   *commands.Link   `cmd:"" group:"lifecycle" help:"Grant a source agent permission to call one or more target agents"`
	Unlink *commands.Unlink `cmd:"" group:"lifecycle" help:"Revoke a source's permission to call one or more target agents"`
	Links  *commands.Links  `cmd:"" group:"lifecycle" help:"Show an agent's outbound + inbound links"`

	// Management groups.
	Agent    AgentCmd    `cmd:"" group:"management" help:"Manage running agents — run, ps, start, stop, rm, chat, prompt, logs, inspect."`
	Image    ImageCmd    `cmd:"" group:"management" help:"Manage agent images — build, push, pull, list, remove, inspect."`
	Bin      BinCmd      `cmd:"" group:"management" help:"Manage binary tools — build, push, pull, list, remove, inspect."`
	Provider ProviderCmd `cmd:"" group:"management" help:"Manage AI provider configuration in ~/.otters/providers.yaml — add, rm, ls, and list available models."`
	Jobs     JobsCmd     `cmd:"" group:"management" help:"Async BIN jobs — run, await, ls, inspect, cancel."`
}

// JobsCmd is the namespace for async BIN jobs dispatched against an
// agent's spawn env. The daemon delivers the completion as a
// synthetic turn in the agent's session, so any agent watching its
// own conversation history sees the result on the next turn.
type JobsCmd struct {
	Run     *commands.JobsRun     `cmd:"" help:"Submit a BIN job to run against an agent's spawn env"`
	Await   *commands.JobsAwait   `cmd:"" help:"Poll a job until completion; print stdout, exit with the BIN's exit code"`
	Ls      *commands.JobsLs      `cmd:"" aliases:"list" help:"List jobs (filter by --agent, --status)"`
	Inspect *commands.JobsInspect `cmd:"" aliases:"desc,describe" help:"Show one job's full state, output, timing"`
	Cancel  *commands.JobsCancel  `cmd:"" aliases:"rm" help:"Cancel a pending or running job"`
	Watch   *commands.JobsWatch   `cmd:"" help:"Stream a job's state changes; closes when the job reaches a terminal status"`
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

// ProviderCmd is the namespace for editing ~/.otters/providers.yaml
// from the CLI. Edits take effect on the running daemon's next call
// to the registry — the lazy-reload contract in
// internal/providers.go means no daemon restart is needed.
type ProviderCmd struct {
	Add    *commands.ProviderAdd  `cmd:"" help:"Interactively or non-interactively add a provider (Anthropic, OpenAI, OpenRouter, Ollama, custom)"`
	Edit   *commands.ProviderEdit `cmd:"" help:"Edit an existing provider (interactive picker + pre-filled form, or scriptable via --name)"`
	Rm     *commands.ProviderRm   `cmd:"" aliases:"remove" help:"Remove one or more providers (interactive form, or scriptable via --name)"`
	Ls     *commands.ProviderLs   `cmd:"" aliases:"list" help:"List configured providers (api keys are masked unless --reveal)"`
	Models *commands.ModelsLs     `cmd:"" help:"List models reachable via configured providers"`
}
