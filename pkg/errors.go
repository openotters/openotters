package pkg

import (
	"errors"

	"google.golang.org/grpc/status"
)

// UnwrapRPC strips the `rpc error: code = ... desc = ...` envelope
// that google.golang.org/grpc prepends to every error returned
// through a client call. The daemon's own message (e.g. `agent "foo"
// not found`) is what the user wants to read; the gRPC code/envelope
// is implementation detail that should stay in logs. Returns err
// unchanged if it isn't a gRPC status error.
func UnwrapRPC(err error) error {
	if err == nil {
		return nil
	}

	st, ok := status.FromError(err)
	if !ok {
		return err
	}

	return errors.New(st.Message())
}
