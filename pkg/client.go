package pkg

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

func DefaultSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".otters", "otters.sock")
}

// Connect dials the daemon over a unix socket with a Bearer token
// attached to every RPC. JWT auth is enforced on every listener
// since the foundation iteration — there is no implicit-trust
// transport.
func Connect(socketPath, token string) (daemonv1.RuntimeClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient("unix:"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(bearerUnary(token)),
		grpc.WithStreamInterceptor(bearerStream(token)),
	)
	if err != nil {
		return nil, nil, err
	}

	return daemonv1.NewRuntimeClient(conn), conn, nil
}

// ConnectTCP dials the daemon over a TCP endpoint with a Bearer
// token attached to every outbound RPC. The daemon's TCP listener
// is intended for remote operators / external clients; the agent
// itself reaches the daemon over the bind-mounted unix socket.
//
// daemonURL should be the daemon's HTTP endpoint (e.g.
// http://127.0.0.1:5050). Scheme is parsed off; host:port is what
// gRPC dials.
func ConnectTCP(daemonURL, token string) (daemonv1.RuntimeClient, *grpc.ClientConn, error) {
	u, err := url.Parse(daemonURL)
	if err != nil {
		return nil, nil, err
	}
	addr := u.Host
	if _, _, splitErr := net.SplitHostPort(addr); splitErr != nil {
		addr = net.JoinHostPort(addr, "80")
	}

	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(bearerUnary(token)),
		grpc.WithStreamInterceptor(bearerStream(token)),
	)
	if err != nil {
		return nil, nil, err
	}

	return daemonv1.NewRuntimeClient(conn), conn, nil
}

// bearerUnary attaches the Authorization header to every unary RPC.
// Returns a still-functional interceptor when token is empty so test
// flows can exercise the dial path without auth — the server's
// interceptor will reject unauthenticated requests with a clear
// Unauthenticated error.
func bearerUnary(token string) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
	) error {
		if token != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func bearerStream(token string) grpc.StreamClientInterceptor {
	return func(
		ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn,
		method string, streamer grpc.Streamer, opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		if token != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		}
		return streamer(ctx, desc, cc, method, opts...)
	}
}
