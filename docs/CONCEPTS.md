# openotters glossary

Definitions and the common misuses to avoid. Read this when a user
needs a term explained or when you're about to phrase something and
aren't sure which noun fits.

## The layer stack

    Agentspec   → grammar rules
    Agentfile   → source document (Dockerfile-shaped)
    Image       → compiled OCI artifact
    Agent       → running instance spawned from an Image

## Terms

**openotters** — the whole project. A local daemon that runs LLM
agents on your machine, driven by the `otters` CLI.

**ottersd** — the daemon process itself. Owns the embedded OCI
registry, manages agent lifecycles, talks to LLM providers.

**Agentspec** — the language specification: what an Agentfile is
*allowed* to contain and how each instruction is interpreted. Lives
in `workspace/agentfile/spec/`.

**Agentfile** — a source document written by a user. Declares the
base, runtime, model, context blocks, tool binaries, etc. *Not*
executable by itself — it's the blueprint.

**Image** (a.k.a. **Agent Image**, artifact type
`application/vnd.openotters.agent.v1`) — the OCI artifact the daemon
builds from an Agentfile. Immutable, addressable by digest, pushable
to any OCI registry. Docker analogue: a Docker image.

**Agent** — a *running instance* of an Image. Has a UUID, friendly
name, runtime subprocess, sandbox workspace on disk, status.
Stopping an Agent does not delete the Image it came from.

**Binary** (a.k.a. **Bin**, artifact type
`application/vnd.openotters.bin.v1`) — an OCI artifact carrying a
static tool binary (one per platform for multi-arch). Referenced
from an Agentfile via `BIN <name> <ref>`; the daemon unpacks the
right platform binary into the agent's sandbox so the LLM can call
it as a tool.

**Model** — an LLM served by a provider. Addressed as
`<provider>/<model>` (e.g. `anthropic/claude-opus-4-7`).
`otters models ls` enumerates what's configured.

**Provider** — an LLM vendor configured in
`~/.otters/providers.yaml`. Holds the API key + endpoint for one
family of models.

## Don't say

- "Build an agent" — you build an *Image*, then run it to get an *Agent*.
- "Pull an agent" — you pull an *Image*.
- "Stop the image" — stopping applies to *Agents*.
- "Mount a volume" — we have *bind mounts*, not named volumes.
