package internal

// runtime_handler_links.go implements the 7 new Connect RPCs the
// agent-linking feature added:
//
// Operator-facing (no extra auth beyond the JWT signature):
//   - LinkAgents, UnlinkAgents, ListAgentLinks
//
// Agent-facing (require an agent token; the target ref the request
// carries must resolve to an id in the caller's JWT.Links):
//   - AgentList, AgentInfo, AgentChat, AgentExec
//
// The auth gate for the agent-facing four lives inside each
// handler (small per-call check) rather than as a separate
// interceptor — three reasons: the gate needs the resolved target
// id, which only the daemon can compute; the gate is only four
// methods so a Connect interceptor would be over-engineered; and
// putting the check in the handler keeps the auth surface readable
// next to the business logic.

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	daemonv1 "github.com/openotters/openotters/api/v1"
	"github.com/openotters/openotters/internal/auth"
)

// ── Operator-facing ─────────────────────────────────────────────

func (h *runtimeHandler) LinkAgents(
	ctx context.Context, req *connect.Request[daemonv1.LinkAgentsRequest],
) (*connect.Response[daemonv1.LinkAgentsResponse], error) {
	restarted, err := h.daemon.LinkAgents(ctx, req.Msg.GetSourceRef(), req.Msg.GetTargetRef())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&daemonv1.LinkAgentsResponse{Restarted: restarted}), nil
}

func (h *runtimeHandler) UnlinkAgents(
	ctx context.Context, req *connect.Request[daemonv1.UnlinkAgentsRequest],
) (*connect.Response[daemonv1.UnlinkAgentsResponse], error) {
	restarted, err := h.daemon.UnlinkAgents(ctx, req.Msg.GetSourceRef(), req.Msg.GetTargetRef())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&daemonv1.UnlinkAgentsResponse{Restarted: restarted}), nil
}

func (h *runtimeHandler) ListAgentLinks(
	ctx context.Context, req *connect.Request[daemonv1.ListAgentLinksRequest],
) (*connect.Response[daemonv1.ListAgentLinksResponse], error) {
	info, err := h.daemon.ListAgentLinks(ctx, req.Msg.GetRef())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&daemonv1.ListAgentLinksResponse{
		Outbound: linkedAgentsToProto(info.Outbound),
		Inbound:  linkedAgentsToProto(info.Inbound),
	}), nil
}

// ── Agent-facing ────────────────────────────────────────────────

func (h *runtimeHandler) AgentList(
	ctx context.Context, _ *connect.Request[daemonv1.AgentListRequest],
) (*connect.Response[daemonv1.AgentListResponse], error) {
	caller, err := requireAgentCaller(ctx)
	if err != nil {
		return nil, err
	}
	links, err := h.daemon.AgentLinkedAgents(ctx, caller.AgentRef)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&daemonv1.AgentListResponse{
		Agents: linkedAgentsToProto(links),
	}), nil
}

func (h *runtimeHandler) AgentInfo(
	ctx context.Context, req *connect.Request[daemonv1.AgentInfoRequest],
) (*connect.Response[daemonv1.AgentInfoResponse], error) {
	target, err := h.requireLinkedTarget(ctx, req.Msg.GetRef())
	if err != nil {
		return nil, err
	}
	info, err := h.daemon.AgentInfo(target.id.String())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(info), nil
}

func (h *runtimeHandler) AgentChat(
	ctx context.Context, req *connect.Request[daemonv1.AgentChatRequest],
) (*connect.Response[daemonv1.AgentChatResponse], error) {
	target, err := h.requireLinkedTarget(ctx, req.Msg.GetRef())
	if err != nil {
		return nil, err
	}
	sessionID := req.Msg.GetSessionId()
	resp, err := h.daemon.ChatWithAgent(ctx, target.name, sessionID, req.Msg.GetPrompt())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&daemonv1.AgentChatResponse{
		Response:  resp,
		SessionId: sessionID,
	}), nil
}

func (h *runtimeHandler) AgentExec(
	ctx context.Context, req *connect.Request[daemonv1.AgentExecRequest],
) (*connect.Response[daemonv1.AgentExecResponse], error) {
	target, err := h.requireLinkedTarget(ctx, req.Msg.GetRef())
	if err != nil {
		return nil, err
	}
	// Exec uses the existing one-shot chat path with a synthetic
	// session id so the target's compactor doesn't pile up
	// rows from cross-agent calls. The "exec session" is single-
	// turn by construction (model writes one reply, no follow-up).
	sessionID := "exec:" + target.id.String()
	resp, err := h.daemon.ChatWithAgent(ctx, target.name, sessionID, req.Msg.GetPrompt())
	if err != nil {
		return nil, err
	}
	// Drop the session so the next exec call starts clean.
	_ = h.daemon.DeleteSession(ctx, target.name, sessionID)
	return connect.NewResponse(&daemonv1.AgentExecResponse{Response: resp}), nil
}

// ── helpers ─────────────────────────────────────────────────────

// requireAgentCaller returns the JWT claims when the caller
// presented an agent token; rejects operator tokens at this
// boundary. The four agent-facing RPCs all start with this check.
func requireAgentCaller(ctx context.Context) (*auth.Claims, error) {
	claims := auth.ClaimsFromContext(ctx)
	if claims == nil || claims.AgentRef == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated,
			errors.New("this RPC requires an agent token"))
	}
	return claims, nil
}

// requireLinkedTarget resolves `ref` to a managedAgent AND
// enforces "target id ∈ caller's JWT.Links". Two failures:
// caller isn't an agent token, or the target isn't linked to the
// caller. Both surface to the model as a clean PermissionDenied
// so the runtime tool can render a useful "you can't call X"
// message.
func (h *runtimeHandler) requireLinkedTarget(
	ctx context.Context, ref string,
) (*managedAgent, error) {
	caller, err := requireAgentCaller(ctx)
	if err != nil {
		return nil, err
	}
	target, err := h.daemon.resolve(ref)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	targetID := target.id.String()
	for _, link := range caller.Links {
		if link == targetID {
			return target, nil
		}
	}
	return nil, connect.NewError(connect.CodePermissionDenied,
		fmt.Errorf("agent %q is not linked to target %q", caller.AgentRef, ref))
}

func linkedAgentsToProto(list []LinkedAgentInfo) []*daemonv1.LinkedAgent {
	out := make([]*daemonv1.LinkedAgent, 0, len(list))
	for _, l := range list {
		out = append(out, &daemonv1.LinkedAgent{
			Id:     l.ID,
			Name:   l.Name,
			Model:  l.Model,
			Status: l.Status,
		})
	}
	return out
}
