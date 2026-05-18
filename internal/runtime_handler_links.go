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
	"github.com/google/uuid"
	daemonv1 "github.com/openotters/openotters/api/v1"
	"github.com/openotters/openotters/internal/auth"
)

// ── Operator-facing ─────────────────────────────────────────────

func (h *runtimeHandler) LinkAgents(
	ctx context.Context, req *connect.Request[daemonv1.LinkAgentsRequest],
) (*connect.Response[daemonv1.LinkAgentsResponse], error) {
	restarted, err := h.daemon.LinkAgents(
		ctx, req.Msg.GetSourceRef(), req.Msg.GetTargetRef(), req.Msg.GetDescription(),
	)
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
	caller, err := requireAgentCaller(ctx)
	if err != nil {
		return nil, err
	}
	target, err := h.requireLinkedTarget(ctx, req.Msg.GetRef())
	if err != nil {
		return nil, err
	}
	info, err := h.daemon.AgentInfo(target.id.String())
	if err != nil {
		return nil, err
	}
	// If the caller's outbound link to this target carries a
	// per-link description, that wins over the target's own
	// `description` label. The link-specific text is the
	// operator's hint to the SOURCE about how to use the target;
	// it should win on the caller-facing surface.
	if override := h.daemon.LinkDescriptionFor(ctx, caller.AgentRef, target.id.String()); override != "" {
		info.Description = override
		if info.Agent != nil {
			info.Agent.Description = override
		}
	}
	return connect.NewResponse(info), nil
}

// AgentExec dispatches the caller's prompt to a linked target and
// returns the target's full reply alongside the session id the
// turn was persisted under. Pass back the returned session_id on
// subsequent calls to preserve history with that target; omit it
// to start a fresh thread.
//
// The daemon mints a self-describing
// `from-agent:<source-id>:<uuid>` session id when the caller
// doesn't supply one, so cross-agent sessions are recognisable
// in the target's session list at a glance.
//
// (agent_chat existed in alpha.82–.84 as a separate threaded
// variant. It was removed in alpha.85 — exec + session_id is the
// single shape now.)
func (h *runtimeHandler) AgentExec(
	ctx context.Context, req *connect.Request[daemonv1.AgentExecRequest],
) (*connect.Response[daemonv1.AgentExecResponse], error) {
	caller, err := requireAgentCaller(ctx)
	if err != nil {
		return nil, err
	}
	target, err := h.requireLinkedTarget(ctx, req.Msg.GetRef())
	if err != nil {
		return nil, err
	}
	sessionID := req.Msg.GetSessionId()
	if sessionID == "" {
		sessionID = "from-agent:" + caller.AgentRef + ":" + uuid.NewString()
	}
	resp, err := h.daemon.ChatWithAgent(ctx, target.name, sessionID, req.Msg.GetPrompt())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&daemonv1.AgentExecResponse{
		Response:  resp,
		SessionId: sessionID,
	}), nil
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
			Id:          l.ID,
			Name:        l.Name,
			Model:       l.Model,
			Status:      l.Status,
			Description: l.Description,
		})
	}
	return out
}
