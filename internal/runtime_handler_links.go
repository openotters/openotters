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
	"github.com/openotters/agentfile/spec"
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

// ── agent self-management ───────────────────────────────────────

// AgentCreate spawns a new agent from an existing image ref. Mounts
// are intentionally absent from the input shape — an agent can't
// open host-filesystem holes by mounting /etc into a child. The
// build-from-source path lives on AgentCreateFromSource, a separate
// capability operators opt into.
func (h *runtimeHandler) AgentCreate(
	ctx context.Context, req *connect.Request[daemonv1.AgentCreateRequest],
) (*connect.Response[daemonv1.AgentCreateResponse], error) {
	if _, err := requireAgentCaller(ctx); err != nil {
		return nil, err
	}
	if req.Msg.GetRef() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("ref is required"))
	}
	createReq := &daemonv1.CreateAgentRequest{
		Name:  req.Msg.GetName(),
		Ref:   req.Msg.GetRef(),
		Model: req.Msg.GetModel(),
		Envs:  req.Msg.GetEnvs(),
		Links: req.Msg.GetLinks(),
	}
	if desc := req.Msg.GetDescription(); desc != "" {
		createReq.Labels = map[string]string{"description": desc}
	}
	resp, err := h.daemon.CreateAgent(ctx, createReq)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&daemonv1.AgentCreateResponse{
		Id:     resp.GetId(),
		Name:   resp.GetName(),
		Status: resp.GetStatus(),
	}), nil
}

// AgentCreateFromSource builds a fresh image from an inline
// Agentfile body, then spawns an agent from it. A more powerful
// capability than AgentCreate — operators attach it deliberately,
// since a compromised orchestrator with this capability can assemble
// any BIN combination available locally.
//
// The Agentfile body must be self-contained (no COPY-from-host); the
// daemon's BuildFromBytes path uses an empty memfs build context. The
// generated image is tagged under `from-agent:<creator>:<uuid>` so
// image_list and the UI can show provenance.
func (h *runtimeHandler) AgentCreateFromSource(
	ctx context.Context, req *connect.Request[daemonv1.AgentCreateFromSourceRequest],
) (*connect.Response[daemonv1.AgentCreateResponse], error) {
	caller, err := requireAgentCaller(ctx)
	if err != nil {
		return nil, err
	}
	body := req.Msg.GetAgentfile()
	if len(body) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("agentfile body is required"))
	}
	tag := "from-agent-" + caller.AgentRef + ":" + uuid.NewString()
	built, err := h.daemon.BuildFromBytes(ctx, body, []string{tag})
	if err != nil {
		return nil, fmt.Errorf("building agent from source: %w", err)
	}
	createReq := &daemonv1.CreateAgentRequest{
		Name:  req.Msg.GetName(),
		Ref:   built.GetRef(),
		Model: req.Msg.GetModel(),
		Envs:  req.Msg.GetEnvs(),
		Links: req.Msg.GetLinks(),
	}
	if desc := req.Msg.GetDescription(); desc != "" {
		createReq.Labels = map[string]string{"description": desc}
	}
	resp, err := h.daemon.CreateAgent(ctx, createReq)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&daemonv1.AgentCreateResponse{
		Id:     resp.GetId(),
		Name:   resp.GetName(),
		Status: resp.GetStatus(),
	}), nil
}

// AgentDelete removes an agent by ref. Any authenticated agent
// caller can delete any agent, including operator-created ones —
// the operator chose to grant this capability when they attached
// agent_delete. Self-delete is allowed at the RPC layer; the
// runtime observes the token revocation on its next call and
// exits.
func (h *runtimeHandler) AgentDelete(
	ctx context.Context, req *connect.Request[daemonv1.AgentDeleteRequest],
) (*connect.Response[daemonv1.AgentDeleteResponse], error) {
	if _, err := requireAgentCaller(ctx); err != nil {
		return nil, err
	}
	if req.Msg.GetRef() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("ref is required"))
	}
	if err := h.daemon.Remove(ctx, req.Msg.GetRef()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&daemonv1.AgentDeleteResponse{}), nil
}

// ImageList returns the trimmed image catalogue (artifact_type =
// AgentArtifactType). Read-only — surfaces nothing the operator
// couldn't already see via `otters image ls`, just in the model-
// friendly ImageRow shape.
func (h *runtimeHandler) ImageList(
	ctx context.Context, _ *connect.Request[daemonv1.ImageListRequest],
) (*connect.Response[daemonv1.ImageListResponse], error) {
	if _, err := requireAgentCaller(ctx); err != nil {
		return nil, err
	}
	rows, err := h.daemon.state.ListImages(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*daemonv1.ImageRow, 0, len(rows))
	for _, r := range rows {
		if r.ArtifactType != spec.AgentArtifactType {
			continue
		}
		out = append(out, &daemonv1.ImageRow{
			Ref:         r.Ref,
			Digest:      r.Digest,
			Description: r.Description,
			Size:        r.Size,
		})
	}
	return connect.NewResponse(&daemonv1.ImageListResponse{Images: out}), nil
}

// BinList is the BIN twin of ImageList.
func (h *runtimeHandler) BinList(
	ctx context.Context, _ *connect.Request[daemonv1.BinListRequest],
) (*connect.Response[daemonv1.BinListResponse], error) {
	if _, err := requireAgentCaller(ctx); err != nil {
		return nil, err
	}
	rows, err := h.daemon.state.ListImages(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*daemonv1.ImageRow, 0, len(rows))
	for _, r := range rows {
		if r.ArtifactType != spec.BinArtifactType {
			continue
		}
		out = append(out, &daemonv1.ImageRow{
			Ref:         r.Ref,
			Digest:      r.Digest,
			Description: r.Description,
			Size:        r.Size,
		})
	}
	return connect.NewResponse(&daemonv1.BinListResponse{Bins: out}), nil
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
