package internal

// daemon_links.go owns the operator-facing Link/Unlink/ListLinks
// orchestration: ref → id resolution, state-store mutation, JWT
// re-issuance, and the auto-restart of a running source agent so
// the new JWT (with the refreshed Links claim) takes effect
// immediately.
//
// The state-store methods (state_links.go) only see UUIDs; the
// daemon owns the resolution + token-rotation half here so the
// state package stays oblivious to the rest of the daemon's
// lifecycle (pool, JWT signing key, etc.).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	daemonv1 "github.com/openotters/openotters/api/v1"
	"github.com/openotters/openotters/internal/auth"
	"go.uber.org/zap"
)

// LinkAgents records source → target and re-issues the source's
// JWT with the refreshed Links claim. When the source is currently
// running it gets transparently stop+started so the new token
// reaches the runtime.
//
// Returns whether the source was restarted; the CLI uses this to
// print a different success message (just "linked" vs "linked +
// restarted A").
func (d *Daemon) LinkAgents(
	ctx context.Context, sourceRef, targetRef, description string,
) (bool, error) {
	source, target, err := d.resolveLinkEndpoints(sourceRef, targetRef)
	if err != nil {
		return false, err
	}

	if addErr := d.state.AgentLinksAdd(
		ctx, source.id.String(), target.id.String(), description,
	); addErr != nil {
		return false, addErr
	}
	return d.refreshAgentTokenAndMaybeRestart(ctx, source)
}

// UnlinkAgents drops source → target. Same auto-restart guarantee
// as LinkAgents: the JWT must reflect the current link set, so
// the source bounces to pick up the shorter claim list.
func (d *Daemon) UnlinkAgents(ctx context.Context, sourceRef, targetRef string) (bool, error) {
	source, target, err := d.resolveLinkEndpoints(sourceRef, targetRef)
	if err != nil {
		return false, err
	}

	if rmErr := d.state.AgentLinksRemove(ctx, source.id.String(), target.id.String()); rmErr != nil {
		return false, rmErr
	}
	return d.refreshAgentTokenAndMaybeRestart(ctx, source)
}

// AgentLinkInfo bundles the agent-graph view: the requested
// agent's own metadata, plus the outbound + inbound edges.
type AgentLinkInfo struct {
	Outbound []LinkedAgentInfo
	Inbound  []LinkedAgentInfo
}

// LinkedAgentInfo is the daemon-side carrier of one edge endpoint.
// Mirrors the wire shape (id/name/model/status/description) but
// stays out of the Connect handler so the daemon can be embedded
// or tested without dragging the proto types in.
//
// Description is the *effective* string the operator + the model
// see: link-specific override when set, otherwise the target's
// `description` label. Empty when neither source has a value.
type LinkedAgentInfo struct {
	ID          string
	Name        string
	Model       string
	Status      string
	Description string
}

// ListAgentLinks returns the directed in / out edges for the
// agent referenced by `ref`. Each entry carries its effective
// description (operator-supplied link override, falling back to
// the target agent's `description` label).
func (d *Daemon) ListAgentLinks(ctx context.Context, ref string) (AgentLinkInfo, error) {
	ma, err := d.resolve(ref)
	if err != nil {
		return AgentLinkInfo{}, err
	}

	outDetails, err := d.state.AgentLinksListOutboundDetails(ctx, ma.id.String())
	if err != nil {
		return AgentLinkInfo{}, err
	}
	inDetails, err := d.state.AgentLinksListInboundDetails(ctx, ma.id.String())
	if err != nil {
		return AgentLinkInfo{}, err
	}

	outbound := make([]LinkedAgentInfo, 0, len(outDetails))
	for _, det := range outDetails {
		info := d.lookupLinkedAgent(det.TargetID)
		info.Description = effectiveLinkDescription(det.Description, info)
		outbound = append(outbound, info)
	}

	inbound := make([]LinkedAgentInfo, 0, len(inDetails))
	for _, det := range inDetails {
		info := d.lookupLinkedAgent(det.SourceID)
		// Inbound description reads "what the SOURCE wrote about
		// this edge" — its operator-supplied rationale for
		// being able to call us. Fall back to the source's own
		// `description` label so the row's never empty.
		info.Description = effectiveLinkDescription(det.Description, info)
		inbound = append(inbound, info)
	}

	return AgentLinkInfo{Outbound: outbound, Inbound: inbound}, nil
}

// AgentList returns the outbound edges for the agent identified by
// agentID (the JWT's AgentRef). Used by the agent-facing AgentList
// RPC; agents only see their outbound graph — they don't
// enumerate inbound (no caller has a need to discover who's
// linked TO them).
func (d *Daemon) AgentLinkedAgents(ctx context.Context, agentID string) ([]LinkedAgentInfo, error) {
	outDetails, err := d.state.AgentLinksListOutboundDetails(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]LinkedAgentInfo, 0, len(outDetails))
	for _, det := range outDetails {
		info := d.lookupLinkedAgent(det.TargetID)
		info.Description = effectiveLinkDescription(det.Description, info)
		out = append(out, info)
	}
	return out, nil
}

// effectiveLinkDescription returns the link-specific override when
// set, otherwise the target's labels["description"]. Centralised
// so every surface (operator UI, agent_list, agent_info) renders
// the same string.
func effectiveLinkDescription(linkOverride string, target LinkedAgentInfo) string {
	if linkOverride != "" {
		return linkOverride
	}
	return target.Description
}

// LinkDescriptionFor returns the operator-supplied description on
// the source → target edge, or "" when no override was set.
// Used by AgentInfo so a caller's view of a target reflects what
// the operator wrote about *this specific* link, not the
// target's generic label.
func (d *Daemon) LinkDescriptionFor(ctx context.Context, sourceID, targetID string) string {
	details, err := d.state.AgentLinksListOutboundDetails(ctx, sourceID)
	if err != nil {
		return ""
	}
	for _, det := range details {
		if det.TargetID == targetID {
			return det.Description
		}
	}
	return ""
}

// AgentInfo returns the wire-shape AgentInfoResponse for an agent
// id: basic metadata + description (from labels) + the list of
// capability names the agent exposes. Used by the agent-facing
// AgentInfo RPC so the caller can decide whether to delegate.
func (d *Daemon) AgentInfo(agentID string) (*daemonv1.AgentInfoResponse, error) {
	ma, ok := d.agents[agentID]
	if !ok {
		return nil, fmt.Errorf("agent %s not found", agentID)
	}
	status := "stopped"
	u, err := uuid.Parse(agentID)
	if err == nil {
		if a, alive := d.pool.Get(u); alive && a != nil {
			status = a.StatusTracker().Get().String()
		}
	}
	description := ""
	if ma.labels != nil {
		description = ma.labels["description"]
	}
	caps := runtimeCapsForExtras(AgentExtras{
		DaemonURL:  d.agentReachableURL(),
		AgentToken: ma.token,
	})
	capNames := make([]string, 0, len(caps))
	for _, c := range caps {
		capNames = append(capNames, c.Name)
	}
	return &daemonv1.AgentInfoResponse{
		Agent: &daemonv1.LinkedAgent{
			Id:     agentID,
			Name:   ma.name,
			Model:  ma.model,
			Status: status,
		},
		Description:  description,
		Capabilities: capNames,
	}, nil
}

// resolveLinkEndpoints resolves both refs and rejects self-links —
// an agent linking to itself doesn't model anything useful and
// would burn a JWT slot.
func (d *Daemon) resolveLinkEndpoints(sourceRef, targetRef string) (*managedAgent, *managedAgent, error) {
	source, err := d.resolve(sourceRef)
	if err != nil {
		return nil, nil, fmt.Errorf("source: %w", err)
	}
	target, err := d.resolve(targetRef)
	if err != nil {
		return nil, nil, fmt.Errorf("target: %w", err)
	}
	if source.id == target.id {
		return nil, nil, errors.New("source and target must differ")
	}
	return source, target, nil
}

// refreshAgentTokenAndMaybeRestart re-mints the source's JWT with
// the current link set, persists it, revokes the prior jti, and
// — when the source is running — stops + starts the agent so the
// runtime spawns with the fresh token. The pool's Restore /
// Start path reads token + token_jti from the agents row, so the
// stop/start dance is enough; no separate token-push channel.
// RefreshAgentTokenAndMaybeRestart re-issues the source's JWT
// against the current agent_links table and bounces the runtime
// when the agent is running. Exported for the agent-facing
// SelfReload handler; LinkAgents / UnlinkAgents continue to use
// it through the unexported alias below.
func (d *Daemon) RefreshAgentTokenAndMaybeRestart(
	ctx context.Context, source *managedAgent,
) (bool, error) {
	return d.refreshAgentTokenAndMaybeRestart(ctx, source)
}

func (d *Daemon) refreshAgentTokenAndMaybeRestart(
	ctx context.Context, source *managedAgent,
) (bool, error) {
	linkIDs, err := d.state.AgentLinksListOutbound(ctx, source.id.String())
	if err != nil {
		return false, err
	}

	if len(d.signingKey) == 0 {
		// No key configured (test / embedded daemon path). The
		// link row landed but we can't rotate; the agent will
		// pick the new links up at its next natural restart.
		return false, nil
	}

	capNames := capabilityNames(runtimeCapsForExtras(AgentExtras{
		DaemonURL:  d.agentReachableURL(),
		AgentToken: "placeholder", // non-empty so daemon-URL-gated caps land
	}))
	newToken, newJTI, err := auth.IssueAgent(d.signingKey, source.id.String(), linkIDs, capNames)
	if err != nil {
		return false, fmt.Errorf("re-issue agent token: %w", err)
	}

	oldJTI := source.tokenJTI
	source.token = newToken
	source.tokenJTI = newJTI

	if persistErr := d.state.UpdateAgentToken(ctx, source.id.String(), newToken, newJTI); persistErr != nil {
		return false, fmt.Errorf("persist refreshed token: %w", persistErr)
	}
	if oldJTI != "" {
		_ = d.state.RevokeToken(ctx, oldJTI, "link-set changed; token re-issued")
	}

	// Stop+start the source if it's currently running so the
	// runtime picks up the new token on its next spawn. Pool's
	// Get returns the live agent; nil means "not running" —
	// nothing to bounce.
	//
	// The pool reuses the same Agent object across Start cycles,
	// and the executor caches the JWT in its deps at construction.
	// Without poking the new token into that cache, the restart
	// spawns the runtime with the stale env value — the runtime
	// then dials the daemon with a revoked JTI and gets 401 on
	// every RPC. SetAgentToken patches the cache in place; we
	// type-assert via tokenSetter because the Agent interface
	// in agentpkg doesn't carry the setter.
	if a, ok := d.pool.Get(source.id); ok && a != nil {
		if setter, hasSetter := a.(interface{ SetAgentToken(string) }); hasSetter {
			setter.SetAgentToken(newToken)
		} else {
			d.logger.Warn("agent does not implement SetAgentToken; runtime will keep stale token until external restart",
				zap.String("id", source.id.String()))
		}
		// Detach the ctx for Stop+Start. SelfReload is called BY the
		// runtime — when Stop kills that runtime, the in-flight RPC's
		// connection dies and the Connect server-side ctx gets
		// cancelled. The follow-up Start would then see a cancelled
		// ctx and fail, leaving the agent stopped. WithoutCancel
		// keeps values + deadline but ignores cancellation so the
		// restart always completes; we then bound it with our own
		// timeout so a hung executor can't pin the goroutine.
		restartCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if stopErr := d.Stop(restartCtx, source.id.String()); stopErr != nil {
			return false, fmt.Errorf("stop source: %w", stopErr)
		}
		if startErr := d.Start(restartCtx, source.id.String()); startErr != nil {
			return false, fmt.Errorf("restart source: %w", startErr)
		}
		return true, nil
	}
	return false, nil
}

// lookupLinkedAgent enriches one agent UUID into the richer
// LinkedAgentInfo shape. Description is the agent's own
// `description` label — the caller layers a link-specific
// override on top via effectiveLinkDescription. Missing rows
// (agent removed after the link was created — shouldn't happen
// given cascade, defensive) surface with empty Name/Model and
// the status "removed".
//
// d.agents access mirrors the existing read pattern (Info(),
// Get() etc.) — no lock today; the pool is the authoritative
// live-state owner and writes to d.agents are serialised through
// CreateAgent / Restore at startup.
func (d *Daemon) lookupLinkedAgent(id string) LinkedAgentInfo {
	u, err := uuid.Parse(id)
	if err != nil {
		return LinkedAgentInfo{ID: id, Status: "removed"}
	}
	ma, ok := d.agents[id]
	if !ok {
		return LinkedAgentInfo{ID: id, Status: "removed"}
	}
	status := "stopped"
	if a, alive := d.pool.Get(u); alive && a != nil {
		status = a.StatusTracker().Get().String()
	}
	desc := ""
	if ma.labels != nil {
		desc = ma.labels["description"]
	}
	return LinkedAgentInfo{
		ID:          id,
		Name:        ma.name,
		Model:       ma.model,
		Status:      status,
		Description: desc,
	}
}
