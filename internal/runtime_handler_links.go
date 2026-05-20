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
	caller, err := requireCapability(ctx, "agent_list")
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
	caller, err := requireCapability(ctx, "agent_info")
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
	caller, err := requireCapability(ctx, "agent_exec")
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

// AgentCreate spawns a new agent from an existing image ref. The
// `links` field is INBOUND to the new agent — each entry is a
// source ref that gains the ability to call the new agent. To
// be able to call the new agent yourself, include your own agent
// ref (or id) in `links`, then call self_reload after this RPC
// returns so your runtime picks up the refreshed JWT.
//
// Mounts are intentionally absent from the input shape — an
// agent can't open host-filesystem holes by mounting /etc into a
// child. The build-from-source path lives on
// AgentCreateFromSource, a separate capability operators opt
// into.
func (h *runtimeHandler) AgentCreate(
	ctx context.Context, req *connect.Request[daemonv1.AgentCreateRequest],
) (*connect.Response[daemonv1.AgentCreateResponse], error) {
	if _, err := requireCapability(ctx, "agent_create"); err != nil {
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
		// `links` is interpreted as inbound (see below) — the new
		// agent gets no outbound links of its own from this RPC.
		// An operator (or the spawning agent via a future tool)
		// can add outbound links later.
	}
	if desc := req.Msg.GetDescription(); desc != "" {
		createReq.Labels = map[string]string{"description": desc}
	}
	resp, err := h.daemon.CreateAgent(ctx, createReq)
	if err != nil {
		return nil, err
	}
	if linkErr := h.persistInboundLinks(ctx, resp.GetId(), req.Msg.GetLinks()); linkErr != nil {
		return nil, linkErr
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
	caller, err := requireCapability(ctx, "agent_create_from_source")
	if err != nil {
		return nil, err
	}
	body := req.Msg.GetAgentfile()
	if len(body) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("agentfile body is required"))
	}
	tag := "from-agent-" + caller.AgentRef + ":" + uuid.NewString()
	if _, buildErr := h.daemon.BuildFromBytes(ctx, body, []string{tag}); buildErr != nil {
		return nil, fmt.Errorf("building agent from source: %w", buildErr)
	}
	// Use the tag WE generated, not built.GetRef(). When the
	// Agentfile body omits the NAME directive, the parser leaves
	// Reference.Name empty and BuildFromBytes serialises that to
	// the malformed ":latest" — passing it into CreateAgent then
	// blows up with "resolving manifest: not found". Our tag is
	// always well-formed because we control its construction.
	createReq := &daemonv1.CreateAgentRequest{
		Name:  req.Msg.GetName(),
		Ref:   tag,
		Model: req.Msg.GetModel(),
		Envs:  req.Msg.GetEnvs(),
		// `links` is inbound — see AgentCreate above.
	}
	if desc := req.Msg.GetDescription(); desc != "" {
		createReq.Labels = map[string]string{"description": desc}
	}
	resp, err := h.daemon.CreateAgent(ctx, createReq)
	if err != nil {
		return nil, err
	}
	if linkErr := h.persistInboundLinks(ctx, resp.GetId(), req.Msg.GetLinks()); linkErr != nil {
		return nil, linkErr
	}
	return connect.NewResponse(&daemonv1.AgentCreateResponse{
		Id:     resp.GetId(),
		Name:   resp.GetName(),
		Status: resp.GetStatus(),
	}), nil
}

// persistInboundLinks records "source → new agent" edges in the
// agent_links table for every ref in the request's `links` field.
// The new agent is the target; each ref is a source that gains
// permission to call it.
//
// The source's JWT is NOT refreshed here — callers that want to
// use the new edge must call self_reload (or be restarted by an
// operator). Mid-tool-call restarts would kill the LLM turn that's
// currently running, so we keep the JWT change explicit.
//
// Unknown source refs fail the whole RPC — better than silently
// dropping links the model asked for. The new agent stays around
// even on failure; the model can agent_delete it if the link set
// is unrecoverable.
func (h *runtimeHandler) persistInboundLinks(
	ctx context.Context, newAgentID string, sources []string,
) error {
	for _, sourceRef := range sources {
		source, err := h.daemon.resolve(sourceRef)
		if err != nil {
			return connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("link source %q: %w", sourceRef, err))
		}
		desc := "spawned via agent_create"
		if linkErr := h.daemon.state.AgentLinksAdd(
			ctx, source.id.String(), newAgentID, desc,
		); linkErr != nil {
			return connect.NewError(connect.CodeInternal,
				fmt.Errorf("persist inbound link %s → %s: %w",
					sourceRef, newAgentID, linkErr))
		}
	}
	return nil
}

// SelfReload re-issues the caller's JWT against the current
// agent_links table and bounces the caller's runtime so the
// refreshed token (and the link set baked into it) takes effect.
// Use after agent_create to actually be able to call the new
// agent — without a self_reload, the JWT in memory still carries
// the original Links claim from agent startup.
//
// Side effect: stop+starts the calling agent. The current LLM
// turn dies with the runtime; the next user turn starts fresh
// on the reloaded agent. Call it as the LAST tool in a turn —
// the agent_create + self_reload sequence is intentionally a
// turn boundary.
func (h *runtimeHandler) SelfReload(
	ctx context.Context, _ *connect.Request[daemonv1.SelfReloadRequest],
) (*connect.Response[daemonv1.SelfReloadResponse], error) {
	caller, err := requireCapability(ctx, "self_reload")
	if err != nil {
		return nil, err
	}
	ma, err := h.daemon.resolve(caller.AgentRef)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	restarted, err := h.daemon.RefreshAgentTokenAndMaybeRestart(ctx, ma)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&daemonv1.SelfReloadResponse{
		Restarted: restarted,
	}), nil
}

// AgentDelete removes an agent by ref. Any authenticated agent
// caller can delete any agent, including operator-created ones —
// the operator chose to grant this capability when they attached
// agent_delete. Self-delete is allowed at the RPC layer; the
// runtime observes the token revocation on its next call and
// exits.
// AgentDelete is the link-scoped delete: target must appear in
// the caller's JWT.Links. Callers that need to delete an
// unlinked agent use AgentDeleteAny, which is granted by
// default today (a future capability wave will gate it).
func (h *runtimeHandler) AgentDelete(
	ctx context.Context, req *connect.Request[daemonv1.AgentDeleteRequest],
) (*connect.Response[daemonv1.AgentDeleteResponse], error) {
	if _, err := requireCapability(ctx, "agent_delete"); err != nil {
		return nil, err
	}
	if req.Msg.GetRef() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("ref is required"))
	}
	target, err := h.requireLinkedTarget(ctx, req.Msg.GetRef())
	if err != nil {
		return nil, err
	}
	if rmErr := h.daemon.Remove(ctx, target.id.String()); rmErr != nil {
		return nil, rmErr
	}
	return connect.NewResponse(&daemonv1.AgentDeleteResponse{}), nil
}

// ── bypass-link variants ────────────────────────────────────────
//
// These four RPCs do the same work as AgentList / AgentInfo /
// AgentExec / AgentDelete but skip the requireLinkedTarget step.
// Gated on their cap name in JWT.Capabilities — an agent without
// the cap can neither see the tool (runtime-side filter) nor
// reach the RPC (daemon-side enforcement).

// AgentListAll returns every agent in the daemon's pool, not
// just the caller's outbound links.
func (h *runtimeHandler) AgentListAll(
	ctx context.Context, _ *connect.Request[daemonv1.AgentListAllRequest],
) (*connect.Response[daemonv1.AgentListAllResponse], error) {
	if _, err := requireCapability(ctx, "agent_list_all"); err != nil {
		return nil, err
	}
	all := h.daemon.AllAgents()
	return connect.NewResponse(&daemonv1.AgentListAllResponse{
		Agents: linkedAgentsToProto(all),
	}), nil
}

// AgentInfoAny inspects any agent by ref, regardless of links.
func (h *runtimeHandler) AgentInfoAny(
	ctx context.Context, req *connect.Request[daemonv1.AgentInfoAnyRequest],
) (*connect.Response[daemonv1.AgentInfoResponse], error) {
	if _, err := requireCapability(ctx, "agent_info_any"); err != nil {
		return nil, err
	}
	if req.Msg.GetRef() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("ref is required"))
	}
	target, err := h.daemon.resolve(req.Msg.GetRef())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	info, err := h.daemon.AgentInfo(target.id.String())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(info), nil
}

// AgentExecAny dispatches a prompt to any agent by ref,
// regardless of links. Session-id semantics match AgentExec.
func (h *runtimeHandler) AgentExecAny(
	ctx context.Context, req *connect.Request[daemonv1.AgentExecAnyRequest],
) (*connect.Response[daemonv1.AgentExecResponse], error) {
	caller, err := requireCapability(ctx, "agent_exec_any")
	if err != nil {
		return nil, err
	}
	if req.Msg.GetRef() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("ref is required"))
	}
	target, err := h.daemon.resolve(req.Msg.GetRef())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
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

// AgentDeleteAny removes any agent by ref. Bypass-link variant
// of AgentDelete.
func (h *runtimeHandler) AgentDeleteAny(
	ctx context.Context, req *connect.Request[daemonv1.AgentDeleteAnyRequest],
) (*connect.Response[daemonv1.AgentDeleteResponse], error) {
	if _, err := requireCapability(ctx, "agent_delete_any"); err != nil {
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
	if _, err := requireCapability(ctx, "image_list"); err != nil {
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
	if _, err := requireCapability(ctx, "bin_list"); err != nil {
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

// requireCapability gates an RPC on the caller having the named
// capability in their JWT.Capabilities claim. Operators (empty
// AgentRef) bypass the cap check — the daemon's auth-side
// distinction between operator and agent tokens is preserved.
// Agent tokens must carry the cap; missing the cap returns
// PermissionDenied with a model-friendly message.
//
// The JWT is the authoritative source — handlers should NOT
// consult the agents table directly. Operator-driven cap grants
// flow through AddAgentCapability which re-issues the JWT
// before this gate will accept the new cap on the next request.
func requireCapability(ctx context.Context, capName string) (*auth.Claims, error) {
	claims := auth.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated,
			errors.New("this RPC requires an authenticated token"))
	}
	if claims.AgentRef == "" {
		// Operator token — open access. Operators don't have
		// per-cap scoping; they're the ones granting caps.
		return claims, nil
	}
	for _, c := range claims.Capabilities {
		if c == capName {
			return claims, nil
		}
	}
	return nil, connect.NewError(connect.CodePermissionDenied,
		fmt.Errorf("agent %q does not have the %q capability — "+
			"operator must grant it via `otters run --cap %s` at create time "+
			"or via the dashboard's Capabilities tab",
			claims.AgentRef, capName, capName))
}

// requireLinkedTarget resolves `ref` to a managedAgent AND
// enforces "target id ∈ caller's JWT.Links". Two failures:
// caller isn't an agent token, or the target isn't linked to the
// caller. Both surface to the model as a clean PermissionDenied
// so the runtime tool can render a useful "you can't call X"
// message.
//
// The JWT is the source of truth for link permissions, NOT the
// agent_links table. Links added at runtime (via agent_create's
// inbound-link request or the operator's `otters link`) only take
// effect after the affected source agent's JWT is re-issued — for
// the caller itself, that means calling self_reload, which
// stop+starts the agent so the new token reaches the runtime.
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

// requireOwnAgent gates an RPC on EITHER an operator token (full
// access across any agent's per-agent state) OR an agent token
// whose identity matches the resolved ref (self-only).
//
// Used by every notes + agent_messages handler — neither surface
// crosses agents, so the link-table lookup that requireLinkedTarget
// performs is unnecessary. Without this check an agent token could
// pass req.Ref = "<another agent>" and read/write that agent's
// state via the daemon's direct-state path.
func (h *runtimeHandler) requireOwnAgent(
	ctx context.Context, ref string,
) (*managedAgent, error) {
	claims := auth.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated,
			errors.New("per-agent state RPC requires an authenticated token"))
	}
	// Agent tokens may omit the ref — the daemon scopes from the
	// JWT in that case so the runtime client doesn't need to thread
	// its own UUID through every call.
	if ref == "" && claims.AgentRef != "" {
		ref = claims.AgentRef
	}
	target, err := h.daemon.resolve(ref)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if claims.AgentRef == "" {
		// Operator token — no scope check.
		return target, nil
	}
	caller, err := h.daemon.resolve(claims.AgentRef)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated,
			fmt.Errorf("agent token references unknown agent %q: %w", claims.AgentRef, err))
	}
	if caller.id != target.id {
		return nil, connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("agent %q cannot access agent %q's per-agent state",
				claims.AgentRef, target.name))
	}
	return target, nil
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
