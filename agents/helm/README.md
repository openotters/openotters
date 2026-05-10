# helm

Generic Helm agent for static chart analysis. Lint, render, inspect,
compare. Doesn't talk to a cluster or registry — that's the
"go"-gated layer.

## Build & run

```sh
otters build agents/helm -t helm:latest
otters run helm:latest --name helm -v $PWD/charts:/charts:ro
otters chat helm
```

## Try it

```
> lint /charts/myapp
> render /charts/myapp with --set image.tag=v2 and --set replicas=3
> show only /charts/myapp/templates/deployment.yaml
> default values vs --set replicas=5 — what changes?
> list dependencies of /charts/myapp
```

Cluster / registry verbs (`install`, `upgrade`, `uninstall`, `push`,
`pull`, `repo update`) need "go" from you and a command echo first.

## Tools

- `helm` (vendored) — primary
- `ls`, `cat` — chart layout / values inspection
- `sh` — temp files, batch loops
