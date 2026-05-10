# kubectl

Generic kubectl agent for read-only Kubernetes investigation. Mount
a kubeconfig and ask anything kubectl can answer about the cluster.

## Build & run

```sh
otters build agents/kubectl -t kubectl:latest
otters run kubectl:latest --name kube -v ~/.kube/config:/.kube/config:ro
otters chat kube
```

## Try it

```
> what's the current context and namespace?
> any pods crashlooping cluster-wide?
> describe deploy/foo in namespace bar — and tail 200 lines of logs
> top pods in kube-system
> events in the last 5 minutes
> show configmap/app-config in default
```

Mutating verbs (`apply`, `edit`, `delete`, `patch`, `scale`,
`rollout`, `cordon`, `drain`, `label`, `annotate`) need "go" from
you, and the agent echoes the command first. Interactive verbs
(`exec`, `port-forward`, `proxy`, `attach`) are refused.

## Tools

- `kubectl` (vendored) — primary
- `jq` (vendored) — filter `-o json` payloads
- `sh`, `cat` — temp files, sanity reads
