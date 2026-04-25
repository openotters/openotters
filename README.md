# openotters

[![Go Reference](https://pkg.go.dev/badge/github.com/openotters/openotters.svg)](https://pkg.go.dev/github.com/openotters/openotters)
[![Go Report Card](https://goreportcard.com/badge/github.com/openotters/openotters)](https://goreportcard.com/report/github.com/openotters/openotters)
[![golangci-lint](https://github.com/openotters/openotters/actions/workflows/golangci.yml/badge.svg)](https://github.com/openotters/openotters/actions/workflows/golangci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE.md)
[![Status: experimental](https://img.shields.io/badge/status-experimental-orange.svg)](#)

> ⚠️ **Experimental.** openotters is in early alpha. APIs, CLI flags, the
> Agentfile grammar, and the on-disk daemon state can all change between
> tagged releases without a deprecation cycle. Don't use it for anything
> you wouldn't be willing to rebuild from scratch tomorrow.

Build, run, and manage AI agents from your terminal. Think Docker, but for autonomous agents.

<!-- TOC -->
* [openotters](#openotters)
  * [Why openotters?](#why-openotters)
  * [Install](#install)
    * [Homebrew (macOS and Linux)](#homebrew-macos-and-linux)
    * [Go (any platform)](#go-any-platform)
  * [Quick start](#quick-start)
  * [Commands](#commands)
  * [Configuration](#configuration)
    * [Providers](#providers)
    * [Daemon socket](#daemon-socket)
  * [Architecture](#architecture)
  * [Daemon gRPC API](#daemon-grpc-api)
  * [License](#license)
<!-- TOC -->

## Why openotters?

Most agent frameworks are libraries — you write code, manage dependencies, wire up tools, handle memory, and deploy
a custom binary. OpenOtters takes a different approach:

**Agents are artifacts, not applications.** You write an [Agentfile](https://github.com/openotters/agentfile)
(like a Dockerfile), build it into an OCI image, and run it. The runtime, tools, and memory are handled for you.

```agentfile
FROM scratch
RUNTIME ghcr.io/openotters/runtime:latest
MODEL anthropic/claude-sonnet-4-20250514
NAME support-bot

CONTEXT SOUL <<EOF
You are a helpful support agent.
EOF

BIN wget ghcr.io/openotters/tools/wget:latest "Fetch URLs"
BIN jq   ghcr.io/openotters/tools/jq:latest   "Process JSON"
```

Then:

```sh
otters run ./Agentfile --name support-bot
otters chat support-bot
```

**What you get:**
- **No code to write** — declare your agent, don't program it
- **Portable artifacts** — push to any OCI registry, pull and run anywhere
- **Sandboxed tools** — agents can only use what you declare, no shell access
- **Built-in memory** — conversation history with automatic compaction
- **Multi-session** — multiple users can chat with the same agent concurrently
- **Composable** — inherit from parent agents with `FROM`, override what you need

## Install

### Homebrew (macOS and Linux)

```sh
brew install openotters/tap/otters
brew services start otters   # starts ottersd in the background
```

### Go (any platform)

```sh
go install github.com/openotters/openotters/cmd/otters@latest
go install github.com/openotters/openotters/cmd/ottersd@latest
```

## Quick start

**1. Configure your LLM provider**

```sh
mkdir -p ~/.otters
cat > ~/.otters/providers.yaml << EOF
providers:
  - name: anthropic
    api-key: ${ANTHROPIC_API_KEY}
EOF
```

**2. Start the daemon** (Homebrew users can skip — `brew services start otters` already did this)

```sh
ottersd serve
```

**3. Run an agent straight from an Agentfile**

```sh
otters run ./Agentfile --name support-bot
otters chat support-bot
```

`otters run` accepts an Agentfile path, a build-context directory, or a registry ref
— it builds if needed, then creates and starts the agent in one step.

**4. Push the built image to a remote registry**

```sh
otters image push ghcr.io/myorg/support-bot:v1.0
```

## Commands

Top-level lifecycle commands are Docker-flavoured shortcuts; each also exists under
the fully-qualified `otters agent …` form.

| Command         | Description                                                         |
|-----------------|---------------------------------------------------------------------|
| `run`           | Run an agent from an Agentfile, build context, or image ref         |
| `ps` / `ls`     | List running agents                                                 |
| `start`         | Start a stopped agent                                               |
| `stop`          | Stop a running agent                                                |
| `rm`            | Remove an agent                                                     |
| `chat`          | Interactive terminal chat with a running agent                      |
| `prompt`        | One-shot prompt; `--schema` / `--schema-file` returns JSON          |
| `logs`          | Print the runtime log file for an agent                             |
| `info`          | Show daemon info (socket, paths, version, agent counts)             |
| `version`       | Print the CLI version                                               |

Management command groups:

| Group    | Subcommands                                                             | Purpose                                                             |
|----------|-------------------------------------------------------------------------|---------------------------------------------------------------------|
| `agent`  | `run`, `ls`, `start`, `stop`, `rm`, `chat`, `prompt`, `logs`, `inspect` | Fully-qualified agent lifecycle.                                    |
| `image`  | `build`, `push`, `pull`, `ls`, `rm`, `inspect` (alias `describe`)       | Agent OCI image management.                                         |
| `bin`    | `build`, `push`, `pull`, `ls`, `rm`, `inspect`                          | Binary tool image management (the artifacts `BIN` directives pull). |
| `models` | `ls`                                                                    | List models advertised by configured providers.                     |

## Configuration

### Providers

LLM API keys live in `~/.otters/providers.yaml`:

```yaml
providers:
  - name: anthropic
    api-key: ${ANTHROPIC_API_KEY}
  - name: openai
    api-key: ${OPENAI_API_KEY}
```

Environment variables are expanded at load time. No secrets ever end up in agent artifacts.

### Daemon socket

Default: `~/.otters/otters.sock`

Override with `--socket` on the CLI or `--socket-path` on the daemon.

## Architecture

```
otters CLI ──gRPC──► ottersd
                              │
                              ├── embedded OCI registry
                              ├── agent lifecycle (create/stop/remove)
                              ├── provider management
                              └── SQLite state persistence
```

The **CLI** is a thin shell — every command delegates to the daemon over a Unix
socket. `otters run ./Agentfile` hands the absolute path to the daemon, which
runs the build itself; the CLI never touches build internals.

The **daemon** manages agent lifecycles using the
[agentfile](https://github.com/openotters/agentfile) library and runs each agent
as a subprocess of the [runtime](https://github.com/openotters/runtime).

## Daemon gRPC API

Proto definition: [`api/v1/daemon.proto`](api/v1/daemon.proto)

| RPC                   | Description                                                       |
|-----------------------|-------------------------------------------------------------------|
| `GetInfo`             | Daemon info: socket, registry addr, data dirs, version, counts    |
| `BuildAgent`          | Build an agent OCI image from an Agentfile on the daemon host     |
| `BuildToolImage`      | Build a multi-arch bin-tool image from local binaries             |
| `SaveAgentImage`      | Import an already-built OCI artifact into the local registry      |
| `PullAgentImage`      | Pull an image from a remote registry into local                   |
| `PushAgentImage`      | Push a local image to a remote registry                           |
| `ListImages`          | List images in the embedded registry                              |
| `RemoveImage`         | Remove an image                                                   |
| `DescribeImage`       | Inspect an image (manifest, config, layers, labels)               |
| `CreateAgent`         | Materialize workspace, start runtime subprocess                   |
| `ListAgents`          | List running and stopped agents                                   |
| `StartAgent`          | Re-start a previously stopped agent                               |
| `StopAgent`           | Stop a running agent                                              |
| `RemoveAgent`         | Remove an agent and its workspace                                 |
| `ChatWithAgent`       | One-shot prompt; returns the final assistant response             |
| `ChatStreamWithAgent` | Streaming chat — per-step events, tool calls, token deltas        |
| `PromptObject`        | One-shot, stateless structured JSON output against a JSON Schema  |
| `ListSessionMessages` | Return persisted messages for an agent/session                    |
| `GetAgentLogs`        | Return the tail of the agent's runtime log file                   |
| `ListModels`          | Enumerate models advertised by configured providers               |

## License

See [LICENSE](LICENSE.md).
