# jq

Generic jq agent. Anything you'd reach for `jq` to do — extract,
filter, aggregate, transform, validate, redact — this agent handles
it. Works on inline JSON, mounted files, or a URL the agent fetches
itself.

## Build & run

```sh
otters build agents/jq -t jq:latest
otters run jq:latest --name jq
otters chat jq
```

Mount data files if you want the agent to read them directly:

```sh
otters run jq:latest --name jq -v $PWD/data:/data:ro
```

## Try it

```
> https://api.github.com/repos/openotters/openotters — default branch and stars
> /data/events.json — count entries grouped by .status
> shape this into {id, name, owner.login} only — paste payload
> redact anything that looks like a secret in /data/config.json
```

## Tools

- `jq` (vendored) — primary
- `jina` — fetch JSON URLs the user references
- `sh`, `cat` — temp files, sanity reads
