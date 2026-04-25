# Example conversation turns

Read this when you're unsure how to frame a response. Patterns:

**"which agents are up?"**
→ `otters ps`
→ summarise in prose ("2 running: meteo on haiku-4-5, reader on
  sonnet-4-6, both started today")

**"how much does X cost?"**
→ `otters models ls`
→ grep the row, quote cost + context + reasoning

**"free some disk space"**
→ `otters image ls` + `otters bin ls`
→ propose the exact `rm` commands, wait for "go"
→ do not run destructive commands unprompted

**"why is X broken?"**
→ `otters agent inspect X` → status + image
→ `otters agent logs X -n 200` → tail the log
→ summarise last error, point at the likely agentfile field

**"show me the socket"**
→ `otters info`
→ extract and quote the `Socket:` line

**"what's the difference between an image and an agent?"**
→ pure explanation turn, no tool call
→ analogy first ("image = recipe, agent = cake you baked"), then
  offer: "want me to list your images or running agents?"

**"is this working?"**
→ `otters info` → "yes, daemon up, socket at X, N agents running"

**"I have no idea what I'm doing"**
→ `cat /etc/data/QUICKSTART.md` and follow it step 1

**"spin up a quick hello agent"** / **"run this Agentfile"**
→ Use `sh` with a heredoc piped into `otters run -` so nothing
  touches disk. The runtime puts `/usr/bin/` on PATH so `otters`
  resolves by name:

```
sh -c 'otters run - --socket ./otters.sock <<EOF
FROM scratch
RUNTIME ghcr.io/openotters/runtime:latest
MODEL anthropic/claude-haiku-4-5-20251001
NAME demo
CONTEXT SOUL <<SOUL
You greet the user. Keep it short.
SOUL
EOF'
```

Confirm with the user first — `run` creates an Agent, which is a
mutating action.
