package commands

import (
	"fmt"
	"os"

	daemonv1 "github.com/openotters/openotters/api/v1"
	"github.com/openotters/openotters/internal/auth"
	"github.com/openotters/openotters/pkg"
	"google.golang.org/grpc"
)

// Daemon is the CLI's connection-target flag set. Two paths:
//
//   - --socket (default ~/.otters/otters.sock or $OTTERS_SOCKET):
//     dial the daemon over a unix socket. Implicit admin — no token
//     needed.
//   - --daemon http://… (or $OTTERSD_URL): dial the daemon over TCP
//     with a Bearer JWT attached to every RPC. Token resolved from
//     $OTTERS_TOKEN, then ~/.otters/credentials.json keyed by the URL.
//
// When both --socket and --daemon are unset, the unix-socket default
// path wins. When --daemon is set, it takes precedence over --socket
// since the user has been explicit about wanting TCP.
type Daemon struct {
	Socket    string `name:"socket" help:"Daemon unix socket path (default: ~/.otters/otters.sock or $OTTERS_SOCKET)" env:"OTTERS_SOCKET" default:""`
	DaemonURL string `name:"daemon" help:"Daemon HTTP/TCP endpoint (e.g. http://127.0.0.1:5050). When set, takes precedence over --socket and uses JWT auth from $OTTERS_TOKEN or ~/.otters/credentials.json." env:"OTTERSD_URL" default:""`
}

func NewDaemon() *Daemon {
	return &Daemon{}
}

func (d *Daemon) Connect() (daemonv1.RuntimeClient, *grpc.ClientConn, error) {
	if d.DaemonURL != "" {
		token, err := loadOperatorToken(d.DaemonURL)
		if err != nil {
			return nil, nil, err
		}
		c, conn, err := pkg.ConnectTCP(d.DaemonURL, token)
		if err != nil {
			return nil, nil, fmt.Errorf("connecting to daemon at %s: %w", d.DaemonURL, err)
		}
		return c, conn, nil
	}

	socketPath := d.Socket
	if socketPath == "" {
		socketPath = pkg.DefaultSocketPath()
	}

	// JWT-on-everything: even local socket access carries a Bearer
	// token. Bootstrapped to credentials.json on the daemon's first
	// boot; CLI looks up by the unix:// URL form of the socket.
	token, err := loadOperatorToken(auth.SocketURL(socketPath))
	if err != nil {
		return nil, nil, err
	}

	c, conn, err := pkg.Connect(socketPath, token)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to daemon: %w", err)
	}

	return c, conn, nil
}

// loadOperatorToken resolves the Bearer token for a TCP daemon
// endpoint. Lookup order:
//
//  1. $OTTERS_TOKEN env (lets CI / scripts pass a token without
//     touching the credentials file).
//  2. ~/.otters/credentials.json keyed by the daemon URL (the
//     bootstrap path — daemon writes this on first TCP boot).
//
// Empty result fails fast with a pointer to both, so the operator
// gets a clean diagnostic instead of a silent Unauthenticated.
func loadOperatorToken(daemonURL string) (string, error) {
	if env := os.Getenv("OTTERS_TOKEN"); env != "" {
		return env, nil
	}
	tok, err := auth.LookupToken(daemonURL)
	if err != nil {
		return "", fmt.Errorf("reading credentials.json: %w", err)
	}
	if tok == "" {
		credPath, _ := auth.CredentialsPath()
		return "", fmt.Errorf(
			"no token for %s — set $OTTERS_TOKEN or ensure %s has an entry "+
				"(daemon writes one on first TCP boot)",
			daemonURL, credPath)
	}
	return tok, nil
}
