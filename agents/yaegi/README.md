# yaegi

Generic yaegi agent. A Go scratchpad: one-liners, stdlib
exploration, small programs the agent runs and shows you.

## Build & run

```sh
otters build agents/yaegi -t yaegi:latest
otters run yaegi:latest --name yaegi
otters chat yaegi
```

## Try it

```
> time.Now().UTC().Add(72*time.Hour).Format(time.RFC3339)?
> regexp.MustCompile(`\d+`).FindAllString("a 1 b 22 c 333", -1)?
> sort + dedupe + count: [4 1 5 1 4 9 2 6 5]
> json.Unmarshal of {"a":1, "b":[1,2]} into map[string]any
> url.QueryEscape on "hello world & goodbye"
```

The interpreter runs in the agent's workspace — sandbox by
convention, not by kernel. The agent avoids `net/http` and
`os/exec` unless you explicitly ask.

## Tools

- `yaegi` (vendored) — primary
- `cat`, `sh` — write / read snippets in the workspace
