# gh

Generic GitHub CLI agent. Repos, PRs, issues, runs, releases, gists,
search — anything `gh` covers. Read-only by default; mutating verbs
need an explicit "go" from you.

## Build & run

```sh
otters build agents/gh -t gh:latest
otters run gh:latest --name gh -e GH_TOKEN=$GH_TOKEN
otters chat gh
```

## Try it

```
> open PRs on openotters/openotters, sorted by updatedAt desc
> stale issues on cli/cli with the "bug" label
> show pr #1234 from kubernetes/kubernetes — body + check status
> latest release of charmbracelet/bubbletea
> who has open prs on my org awaiting review?
```

## Tools

- `gh` (vendored) — primary
- `jq` (vendored) — shape `--json` output
- `sh`, `cat` — temp files, sanity reads
