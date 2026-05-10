# crane

Generic crane agent for OCI registry inspection. Digests, manifests,
configs, layer sizes, tags, platforms — anything crane can read.

## Build & run

```sh
otters build agents/crane -t crane:latest
otters run crane:latest --name crane
otters chat crane
```

For private registries:

```sh
otters run crane:latest --name crane \
  -v ~/.docker/config.json:/.docker/config.json:ro
```

## Try it

```
> digest of ghcr.io/openotters/runtime:latest
> manifest of docker.io/library/alpine:3.20 — top 3 layers by size
> tags on ghcr.io/openotters/agents/pinger
> entrypoint and cmd of ghcr.io/openotters/runtime:latest
> is ghcr.io/openotters/runtime:v1 multi-arch? which platforms?
> diff ghcr.io/me/x:v1 vs :v2
```

Mutating verbs (`push`, `delete`, `copy`, `tag`, `mutate`) need
"go" from you and a command echo first.

## Tools

- `crane` (vendored) — primary
- `jq` (vendored) — filter manifest / config JSON
- `sh`, `cat` — temp files, intermediate inspection
