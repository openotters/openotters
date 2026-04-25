package pkg_test

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/openotters/openotters/pkg"
)

func TestUnwrapRPC_NilStaysNil(t *testing.T) {
	t.Parallel()

	if got := pkg.UnwrapRPC(nil); got != nil {
		t.Fatalf("UnwrapRPC(nil) = %v, want nil", got)
	}
}

func TestUnwrapRPC_PlainErrorPassthrough(t *testing.T) {
	t.Parallel()

	plain := errors.New("not a gRPC error")

	got := pkg.UnwrapRPC(plain)
	if !errors.Is(got, plain) {
		t.Fatalf("UnwrapRPC pass-through changed the error: got %v want %v", got, plain)
	}
}

func TestUnwrapRPC_StripsRPCEnvelope(t *testing.T) {
	t.Parallel()

	wrapped := status.Error(codes.NotFound, `agent "foo" not found`)

	got := pkg.UnwrapRPC(wrapped)
	if got == nil {
		t.Fatal("UnwrapRPC returned nil for a status error")
	}

	if got.Error() != `agent "foo" not found` {
		t.Errorf("UnwrapRPC = %q, want bare daemon message", got.Error())
	}

	// The transport envelope is what we strip; make sure it really is gone.
	if strings.Contains(got.Error(), "rpc error") || strings.Contains(got.Error(), "code =") {
		t.Errorf("envelope leaked into UnwrapRPC output: %q", got.Error())
	}
}
