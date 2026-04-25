// Package internal hosts the ottersd daemon implementation: the
// embedded OCI registry, the agent pool and lifecycle manager, the
// SQLite-backed state store, the gRPC server, the LLM provider
// registry, and the catwalk model catalogue. None of these are part
// of the published surface — external callers should rely on the
// gRPC API (api/v1) plus the thin client in pkg/.
package internal
