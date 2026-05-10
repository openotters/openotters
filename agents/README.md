# Demo agents

One Agentfile per vendored tool, each generic over that tool's whole
surface area. The agent for `jq` handles any jq question (extract /
filter / aggregate / transform / redact / validate); the agent for
`kubectl` handles any read-only kubectl question; and so on. None of
them are pinned to one task.

The root-level `Agentfile` (`otter`) is the meta-agent that drives
the otters CLI itself; it lives at the repo root rather than in this
directory because it ships with openotters proper.

## Agents

| Agent | Vendored tool | Helpers | Scope |
| --- | --- | --- | --- |
| [`crane`](./crane/)     | `crane`   | `jq`, `sh`, `cat` | Read-only OCI registry inspection |
| [`fd`](./fd/)           | `fd`      | `cat`, `ls`, `sh` | Locate files / dirs by any predicate |
| [`gh`](./gh/)           | `gh`      | `jq`, `sh`, `cat` | GitHub CLI, read-only by default |
| [`helm`](./helm/)       | `helm`    | `ls`, `cat`, `sh` | Static Helm analysis (lint / template / show) |
| [`jq`](./jq/)           | `jq`      | `jina`, `sh`, `cat` | Any jq operation on JSON |
| [`kubectl`](./kubectl/) | `kubectl` | `jq`, `sh`, `cat` | Read-only Kubernetes investigation |
| [`pandoc`](./pandoc/)   | `pandoc`  | `ls`, `sh`        | Document conversion across pandoc formats |
| [`rg`](./rg/)           | `rg`      | `cat`, `sh`       | Recursive content search |
| [`yaegi`](./yaegi/)     | `yaegi`   | `cat`, `sh`       | Go interpreter scratchpad |
| [`yq`](./yq/)           | `yq`      | `cat`, `sh`       | Any yq operation on YAML |

Each Agentfile has a `CONTEXT SCENARIOS` block — that's the
fastest way to see the surface that agent covers.

Mutating verbs are gated on user "go" everywhere: `gh pr merge`,
`kubectl apply`, `helm install`, `crane push`, `yq -i`, etc. The
agent echoes the exact command before running anything that mutates
state.

## Provider configuration

All agents pin `MODEL anthropic/claude-haiku-4-5-20251001` for
consistency. Anthropic key in `~/.otters/providers.yaml`:

```yaml
providers:
  - name: anthropic
    api-key: ${ANTHROPIC_API_KEY}
```

To swap a vendor (OpenAI, OpenRouter, or any OpenAI-compatible
endpoint like Ollama / Groq / Gemini / Mistral / xAI), change the
`MODEL` line and add the matching provider entry.

## Tighter, single-task examples

For one-job demo agents (a connectivity probe, a weather bot, a
JSON-strict greeter), see `workspace/agentfile/demo/` in the
sibling agentfile module.
