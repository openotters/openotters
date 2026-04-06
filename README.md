# openotters

[![Go Reference](https://pkg.go.dev/badge/github.com/openotters/cli.svg)](https://pkg.go.dev/github.com/openotters/cli)
[![Go Report Card](https://goreportcard.com/badge/github.com/openotters/cli)](https://goreportcard.com/report/github.com/openotters/cli)
[![golangci-lint](https://github.com/openotters/openotters/actions/workflows/golangci.yml/badge.svg)](https://github.com/openotters/openotters/actions/workflows/golangci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE.md)

Build, run, and manage AI agents from your terminal. Think Docker, but for autonomous agents.

<!-- TOC -->
* [openotters](#openotters)
  * [Why openotters?](#why-openotters)
  * [Install](#install)
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
openotters build -f Agentfile
openotters run support-bot:latest
openotters chat support-bot
```

**What you get:**
- **No code to write** — declare your agent, don't program it
- **Portable artifacts** — push to any OCI registry, pull and run anywhere
- **Sandboxed tools** — agents can only use what you declare, no shell access
- **Built-in memory** — conversation history with automatic compaction
- **Multi-session** — multiple users can chat with the same agent concurrently
- **Composable** — inherit from parent agents with `FROM`, override what you need

## Install

```sh
go install github.com/openotters/cli/cmd/openotters@latest
go install github.com/openotters/cli/cmd/openotters-daemon@latest
```

## Quick start

**1. Configure your LLM provider**

```sh
mkdir -p ~/.openotters
cat > ~/.openotters/providers.yaml << EOF
providers:
  - name: anthropic
    api-key: ${ANTHROPIC_API_KEY}
EOF
```

**2. Start the daemon** (keep it running in a separate terminal)

```sh
openotters-daemon
```

**3. Build and run an agent**

```sh
openotters build -f Agentfile
openotters run support-bot:latest
openotters chat support-bot
```

**4. Push to a registry**

```sh
openotters push ghcr.io/myorg/support-bot:v1.0
```

## Commands

| Command          | Description                                                         |
|------------------|---------------------------------------------------------------------|
| `build`          | Build an OCI artifact from an Agentfile and save to daemon registry |
| `pull`           | Pull an agent image from a remote registry                          |
| `push`           | Push a local image to a remote registry                             |
| `run`            | Create and start an agent from a registry image                     |
| `ps`             | List running agents                                                 |
| `stop`           | Stop a running agent                                                |
| `rm`             | Remove an agent                                                     |
| `chat`           | Interactive terminal chat with a running agent                      |
| `image ls`       | List images in the local registry                                   |
| `image rm`       | Remove an image                                                     |
| `image describe` | Inspect an image (manifest, layers, config)                         |

## Configuration

### Providers

LLM API keys live in `~/.openotters/providers.yaml`:

```yaml
providers:
  - name: anthropic
    api-key: ${ANTHROPIC_API_KEY}
  - name: openai
    api-key: ${OPENAI_API_KEY}
```

Environment variables are expanded at load time. No secrets ever end up in agent artifacts.

### Daemon socket

Default: `~/.openotters/openotters.sock`

Override with `--socket` on the CLI or `--socket-path` on the daemon.

## Architecture

```
openotters CLI ──gRPC──► openotters-daemon
                              │
                              ├── embedded OCI registry
                              ├── agent lifecycle (create/stop/remove)
                              ├── provider management
                              └── SQLite state persistence
```

The **CLI** is a thin shell — all commands delegate to the daemon via gRPC over a Unix socket, except `build` which
runs client-side (needs access to the local filesystem).

The **daemon** manages agent lifecycles using the [agentfile](https://github.com/openotters/agentfile) library and
runs agents with the [runtime](https://github.com/openotters/runtime).

## Daemon gRPC API

Proto definition: [`api/v1/daemon.proto`](api/v1/daemon.proto)

| RPC                   | Description                                         |
|-----------------------|-----------------------------------------------------|
| `SaveAgentImage`      | Import a built OCI artifact into the local registry |
| `PullAgentImage`      | Pull an image from a remote registry into local     |
| `PushAgentImage`      | Push a local image to a remote registry             |
| `ListImages`          | List images in the embedded registry                |
| `RemoveImage`         | Remove an image                                     |
| `DescribeImage`       | Inspect an image                                    |
| `CreateAgent`         | Pull image, materialize workspace, start runtime    |
| `ListAgents`          | List running/stopped agents                         |
| `StopAgent`           | Stop an agent                                       |
| `RemoveAgent`         | Remove an agent and its workspace                   |
| `ChatWithAgent`       | Send a prompt to a running agent                    |
| `ChatStreamWithAgent` | Streaming chat with tool call events                |

## License

See [LICENSE](LICENSE.md).
