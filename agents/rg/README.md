# rg

Generic ripgrep agent. Mount a tree, ask any rg-shaped question.

## Build & run

```sh
otters build agents/rg -t rg:latest
otters run rg:latest --name rg -v $PWD:/repo:ro
otters chat rg
```

## Try it

```
> where is CreateAgent defined under /repo?
> who calls Resolve(...) — exclude tests
> count occurrences of TODO in /repo, by file
> match /func\s+\w+Handler/ in Go files only
> find files containing "deprecated" but not in vendor/
```

## Tools

- `rg` (vendored) — primary
- `cat` — fuller context after a hit
- `sh` — pipe rg through awk / sort / uniq for summaries
