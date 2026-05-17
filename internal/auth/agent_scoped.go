package auth

import (
	"context"
	"errors"

	"connectrpc.com/connect"
)

// AgentScopedInterceptor rejects any call that doesn't arrive with
// an agent-scoped JWT (i.e. operator tokens, or no auth). Plugged
// into Connect's NewAgentStateHandler so the AgentState service can
// trust ClaimsFromContext(ctx).AgentRef on every call without a
// per-handler check.
//
// The actual scoping (using the AgentRef as the agent_id for table
// reads/writes) happens inside each handler — the interceptor's
// only job is to reject operator tokens. AgentRef = "" means the
// token was issued via IssueOperator; AgentRef = "<uuid>" means
// IssueAgent.
type AgentScopedInterceptor struct{}

func NewAgentScopedInterceptor() AgentScopedInterceptor { return AgentScopedInterceptor{} }

func (AgentScopedInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := requireAgentToken(ctx); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (AgentScopedInterceptor) WrapStreamingClient(
	next connect.StreamingClientFunc,
) connect.StreamingClientFunc {
	return next
}

func (AgentScopedInterceptor) WrapStreamingHandler(
	next connect.StreamingHandlerFunc,
) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := requireAgentToken(ctx); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

func requireAgentToken(ctx context.Context) error {
	claims := ClaimsFromContext(ctx)
	if claims == nil || claims.AgentRef == "" {
		return connect.NewError(
			connect.CodeUnauthenticated,
			errors.New("AgentState requires an agent token; operator tokens cannot read or write per-agent state"),
		)
	}
	return nil
}

// AgentIDFromContext returns the agent_id the AgentScopedInterceptor
// validated for the current call. Returns "" when no agent token
// reached the interceptor — handlers should treat that as an
// internal error since the interceptor would have rejected the call.
func AgentIDFromContext(ctx context.Context) string {
	if c := ClaimsFromContext(ctx); c != nil {
		return c.AgentRef
	}
	return ""
}
