# yq

Generic yq agent. Read, merge, patch, validate, convert YAML.
Mount your config dir; ask anything yq can answer.

## Build & run

```sh
otters build agents/yq -t yq:latest
otters run yq:latest --name yq -v $PWD/configs:/configs
otters chat yq
```

## Try it

```
> show .services.web.image in /configs/compose.yaml
> merge /configs/base.yaml and /configs/override.yaml
> redact anything secret-looking in /configs/values.yaml
> set .replicas = 5 in /configs/values.yaml in place
> convert /configs/values.yaml to JSON
```

In-place edits (`-i`) need an explicit "go" from you, and the agent
echoes the full `yq` command before running it.

## Tools

- `yq` (vendored) — primary
- `sh`, `cat` — temp files, sanity reads
