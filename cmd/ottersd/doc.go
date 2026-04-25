// Command ottersd is the openotters daemon — owns the embedded OCI
// registry, the agent pool, the LLM provider registry, and the gRPC
// API the otters CLI talks to. Run it once per host; the otters CLI
// connects via the unix socket under ~/.otters/.
package main
