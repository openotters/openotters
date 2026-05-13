// White-box tests for the bearer-token interceptor closures. The
// public Connect / ConnectTCP wrap these and there is no other way
// to exercise the metadata-attachment branch — black-box tests can
// observe the interceptors only through a live gRPC dial, which is
// heavier than necessary for the assertion we want.
package pkg

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestBearerUnary_AttachesAuthorizationWithToken(t *testing.T) {
	t.Parallel()

	var captured context.Context
	invoker := func(
		ctx context.Context, _ string, _, _ any,
		_ *grpc.ClientConn, _ ...grpc.CallOption,
	) error {
		captured = ctx
		return nil
	}

	icpt := bearerUnary("secret")
	if err := icpt(context.Background(), "/m", nil, nil, nil, invoker); err != nil {
		t.Fatalf("bearerUnary invoke: %v", err)
	}

	md, ok := metadata.FromOutgoingContext(captured)
	if !ok {
		t.Fatal("no outgoing metadata attached")
	}
	if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer secret" {
		t.Errorf("authorization = %v, want [\"Bearer secret\"]", got)
	}
}

func TestBearerUnary_NoAuthorizationWhenTokenEmpty(t *testing.T) {
	t.Parallel()

	var captured context.Context
	invoker := func(
		ctx context.Context, _ string, _, _ any,
		_ *grpc.ClientConn, _ ...grpc.CallOption,
	) error {
		captured = ctx
		return nil
	}

	icpt := bearerUnary("")
	if err := icpt(context.Background(), "/m", nil, nil, nil, invoker); err != nil {
		t.Fatalf("bearerUnary invoke: %v", err)
	}

	if md, ok := metadata.FromOutgoingContext(captured); ok {
		if got := md.Get("authorization"); len(got) > 0 {
			t.Errorf("empty token: authorization should be absent, got %v", got)
		}
	}
}

func TestBearerUnary_PropagatesInvokerError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("downstream blew up")
	invoker := func(
		_ context.Context, _ string, _, _ any,
		_ *grpc.ClientConn, _ ...grpc.CallOption,
	) error {
		return sentinel
	}

	icpt := bearerUnary("anything")
	err := icpt(context.Background(), "/m", nil, nil, nil, invoker)
	if !errors.Is(err, sentinel) {
		t.Fatalf("bearerUnary err = %v, want %v", err, sentinel)
	}
}

func TestBearerStream_AttachesAuthorizationWithToken(t *testing.T) {
	t.Parallel()

	var captured context.Context
	// streamer returns nil stream + nil err — fine here because the
	// closure under test only mutates the ctx; the test inspects the
	// captured ctx and doesn't try to use the stream.
	streamer := func(
		ctx context.Context, _ *grpc.StreamDesc, _ *grpc.ClientConn,
		_ string, _ ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		captured = ctx
		return nil, nil //nolint:nilnil // closure under test only mutates ctx; stream is unused
	}

	icpt := bearerStream("stream-secret")
	if _, err := icpt(context.Background(), &grpc.StreamDesc{}, nil, "/m", streamer); err != nil {
		t.Fatalf("bearerStream invoke: %v", err)
	}

	md, ok := metadata.FromOutgoingContext(captured)
	if !ok {
		t.Fatal("no outgoing metadata attached")
	}
	if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer stream-secret" {
		t.Errorf("authorization = %v, want [\"Bearer stream-secret\"]", got)
	}
}

func TestBearerStream_NoAuthorizationWhenTokenEmpty(t *testing.T) {
	t.Parallel()

	var captured context.Context
	// See sibling test above for why nil stream + nil err is OK.
	streamer := func(
		ctx context.Context, _ *grpc.StreamDesc, _ *grpc.ClientConn,
		_ string, _ ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		captured = ctx
		return nil, nil //nolint:nilnil // closure under test only mutates ctx; stream is unused
	}

	icpt := bearerStream("")
	if _, err := icpt(context.Background(), &grpc.StreamDesc{}, nil, "/m", streamer); err != nil {
		t.Fatalf("bearerStream invoke: %v", err)
	}

	if md, ok := metadata.FromOutgoingContext(captured); ok {
		if got := md.Get("authorization"); len(got) > 0 {
			t.Errorf("empty token: authorization should be absent, got %v", got)
		}
	}
}
