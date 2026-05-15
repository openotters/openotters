# kubectl

kubectl agent for Kubernetes investigation and gated mutations.
Mount a kubeconfig, then ask anything kubectl can answer about the
cluster — workloads, nodes, events, configs, logs. Reads project
JSON through `jq` before reaching the model; long rollouts and
drains run as async jobs you can watch live.

## Build & run

```sh
otters image build agents/kubectl -t kubectl:latest
otters run kubectl:latest --name kube -v ~/.kube/config:/kubeconfig.yaml:ro
otters chat kube
```

`KUBECONFIG` defaults to `/kubeconfig.yaml` inside the agent. If
your kubeconfig lives elsewhere, mount it to the same target:

```sh
otters run kubectl:latest -v ./prod.kubeconfig:/kubeconfig.yaml:ro
```

## Try it

```
> what's the current context and namespace?
> any pods crashlooping cluster-wide?
> show me top 10 pods by memory across all namespaces
> events on pod foo in namespace bar — chronological
> configmap/app-config in default as KEY=VALUE
> secret keys for db-credentials (don't show values)
> roll out deploy/api in prod, wait until ready, tell me when it's done
> drain node-3 — watch progress live
```

Reads run as a single `sh -c 'kubectl … -o json | jq …'` pipeline
so only projected columns reach the model. Long-running work
(rollouts, drains, polls with deadlines) submits as a `job_submit`
and uses `job_wait` (for the result) or `job_watch` (for live
stdout).

## Gating

- **Mutating** (`apply`, `create`, `delete`, `patch`, `edit`,
  `scale`, `rollout`, `restart`, `cordon`, `drain`, `uncordon`,
  `label`, `annotate`, `taint`) — the agent echoes the verbatim
  command and waits for explicit "go".
- **Interactive** (`exec`, `port-forward`, `proxy`, `attach`) —
  refused; the agent has no TTY.
- **Secrets** — only keys are returned, never values.

## Tools

- `kubectl` — primary
- `sh` — composes one-call pipelines
- `jq` — projects JSON; `yq` for user-supplied YAML
- `grep`, `head`, `tail`, `sort` — narrowing inside pipelines
- `date`, `sleep` — deadline-bounded polling jobs
- `yaegi` — embedded Go for transforms jq can't express

## Async jobs

The runtime exposes `job_submit`, `job_status`, `job_wait`,
`job_watch`, `job_list`, `job_cancel`. The agent picks based on
expected wall time:

| Wall time                    | Pattern                                  |
|------------------------------|------------------------------------------|
| < 5 s read                   | `sh -c '<pipeline>'` inline              |
| ≥ 5 s, deterministic         | `job_submit` + `job_wait`                |
| ≥ 5 s, watch progress        | `job_submit` + `job_watch`               |
| State-change polling         | `job_submit` (loop inside) + `job_watch` |
