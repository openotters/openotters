// Package pkg is the small client surface external programs use to
// talk to a local ottersd instance. It exposes:
//
//   - DefaultSocketPath — the conventional unix-socket location under
//     `~/.otters/`.
//   - Connect — a thin grpc.NewClient wrapper that returns the daemon
//     RuntimeClient plus the underlying *grpc.ClientConn so the caller
//     can defer Close.
//   - UnwrapRPC — strips the `rpc error: code = ... desc = ...`
//     envelope that google.golang.org/grpc prepends to every error
//     so user-facing tooling shows the daemon's own message.
//
// Daemon-internal types live in the (unexported) `internal` package
// tree; only this package is part of the stable API.
package pkg
