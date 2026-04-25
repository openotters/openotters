// Package commands holds the kong-driven subcommands of the otters
// CLI: agent / image / bin lifecycle, chat, prompt, logs, info, and
// the management groupings that mirror them. Each command is a thin
// gRPC client over the daemon's unix socket.
package commands
