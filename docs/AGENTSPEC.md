# Agentfile grammar

Read this when a user asks "how do I write an Agentfile?" or needs
syntax help composing one. Canonical source:
github.com/openotters/agentfile/blob/main/AGENTFILE-v1.0.0.md
— fetch with `jina` for exhaustive detail.

## Top-level directives

```
FROM <image>                 — base image. `FROM scratch` for no base.
RUNTIME <ref>                — OCI ref of the runtime binary.
MODEL <provider/name>        — default LLM.
NAME <ident>                 — default agent name / image tag stem.

CONTEXT <NAME> ["desc"] file://path
CONTEXT <NAME> ["desc"] <<EOF
  body baked into the system prompt
  EOF
                             — named context layer. Multiple blocks
                               encouraged; one concern per block.

CONFIG <key>[=<value>] ["desc"]
CONFIG <key>! ["desc"]       — runtime config knob. `!` = required.

BIN <name> <image-ref> ["desc"] [<<USAGE]
                             — tool binary. USAGE heredoc is the
                               LLM-visible tool description.

ADD <src> <dst> ["desc"]     — copy a file from the build context
                               into /etc/data/ at build time.
                               Not a runtime mount.

EXEC ["arg1", "arg2", …]     — override runtime argv. Rarely needed.

LABEL <key>=<value>          — OCI manifest annotation.

ARG <key>[=<default>]        — build-time argument.
```

## Rules

- Keywords are case-sensitive; must be the first token on the line.
- Heredoc delimiter is always `<<EOF` / `EOF`.
- Comments: `#` to end of line.
- CONTEXT bodies stack into a single generated AGENT.md the runtime
  loads on every turn; keep each block focused.
- Mount paths: use `otters run -v HOST:TARGET[:DESC]` at runtime
  (not in the Agentfile). A MOUNTS.md context layer is written
  automatically.

## Minimal skeleton

```
FROM scratch
RUNTIME ghcr.io/openotters/runtime:latest
MODEL anthropic/claude-haiku-4-5-20251001
NAME hello
CONTEXT SOUL <<EOF
You greet the user. Keep it short.
EOF
```

When a user wants an Agentfile, start from that and ask what
CONTEXT / BIN / CONFIG they need.
