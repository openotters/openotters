# Quickstart walkthrough

Read this when a user signals they're brand new: "hi", "help",
"what is this?", "I have no idea what I'm doing".

## Elevator pitch (say this first)

> openotters lets you run LLM agents locally. You describe them in
> an Agentfile, compile that into an Image, then run the Image to
> get a live Agent you can chat with or have do work for you. I'm
> here to drive the CLI and explain.

## Five steps, one at a time

Pause after each to confirm the user wants to continue.

1. **Daemon alive?** → `otters info`. Relay the socket path,
   registry address, agent counts. If it errors, ottersd isn't
   running — tell them `ottersd serve` and stop.

2. **Pick a model.** → `otters models ls`. Highlight 2–3 good
   starts — cheap + reasoning-capable is usually best. Quote the
   `<provider>/<model>` ref they'd paste into an Agentfile.

3. **What's already on disk?** → `otters image ls` and
   `otters bin ls` back-to-back. One-paragraph summary.

4. **First run.** → Pick the simplest existing image and propose
   `otters run <image>`. Explain the output (id, name, status,
   addr) and that `otters chat <name>` opens a session.

5. **Branch.** Offer three next steps:
   - Build your own agent (walk through a minimal Agentfile).
   - Mount a host directory (`-v HOST:/target:DESC` on `otters run`).
   - Debug an agent (`otters agent logs X -n 200` + `inspect`).

If the user asks something specific mid-walkthrough, skip ahead.
