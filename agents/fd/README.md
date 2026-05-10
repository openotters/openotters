# fd

Generic fd agent. Locate files and directories by name, type,
extension, glob, age, or depth.

## Build & run

```sh
otters build agents/fd -t fd:latest
otters run fd:latest --name fd -v $PWD:/repo:ro
otters chat fd
```

## Try it

```
> all .go files under /repo
> dirs named "test" anywhere in /repo
> files modified in the last 24 hours
> top-level only — list dirs
> include hidden + ignored, find any .env files
```

fd respects `.gitignore` by default; pass "include ignored" or
"include hidden" in your request to broaden.

## Tools

- `fd` (vendored) — primary
- `cat`, `ls` — inspect what fd surfaced
- `sh` — pipe through `wc` / `xargs` / `head`
