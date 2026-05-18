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

	"github.com/google/uuid"
	daemonv1 "github.com/openotters/openotters/api/v1"
	"github.com/openotters/openotters/internal/auth"
)

// LinkAgents records source → target and re-issues the source's
// JWT with the refreshed Links claim. When the source is currently
// running it gets transparently stop+started so the new token
// reaches the runtime.
//
// Returns whether the source was restarted; the CLI uses this to
// print a different success message (just "linked" vs "linked +
// restarted A").
func (d *Daemon) LinkAgents(ctx context.Context, sourceRef, targetRef string) (bool, error) {
	source, target, err := d.resolveLinkEndpoints(sourceRef, targetRef)
	if err != nil {
		return false, err
	}

	if addErr := d.state.AgentLinksAdd(ctx, source.id.String(), target.id.String()); addErr != nil {
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
// Mirrors the wire shape (id/name/model/status) but stays out of
// the Connect handler so the daemon can be embedded / tested
// without dragging the proto types in.
type LinkedAgentInfo struct {
	ID     string
	Name   string
	Model  string
	Status string
}

// ListAgentLinks returns the directed in / out edges for the
// agent referenced by `ref`. Resolves once, then enriches each
// edge endpoint via the existing pool / state lookup so the
// caller doesn't need to round-trip again for human-readable
// names.
func (d *Daemon) ListAgentLinks(ctx context.Context, ref string) (AgentLinkInfo, error) {
	ma, err := d.resolve(ref)
	if err != nil {
		return AgentLinkInfo{}, err
	}

	outboundIDs, err := d.state.AgentLinksListOutbound(ctx, ma.id.String())
	if err != nil {
		return AgentLinkInfo{}, err
	}
	inboundIDs, err := d.state.AgentLinksListInbound(ctx, ma.id.String())
	if err != nil {
		return AgentLinkInfo{}, err
	}

	return AgentLinkInfo{
		Outbound: d.lookupLinkedAgents(outboundIDs),
		Inbound:  d.lookupLinkedAgents(inboundIDs),
	}, nil
}

// AgentList returns the outbound edges for the agent identified by
// agentID (the JWT's AgentRef). Used by the agent-facing AgentList
// RPC; agents only see their outbound graph — they don't
// enumerate inbound (no caller has a need to discover who's
// linked TO them).
func (d *Daemon) AgentLinkedAgents(ctx context.Context, agentID string) ([]LinkedAgentInfo, error) {
	outboundIDs, err := d.state.AgentLinksListOutbound(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return d.lookupLinkedAgents(outboundIDs), nil
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

	newToken, newJTI, err := auth.IssueAgent(d.signingKey, source.id.String(), linkIDs)
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
	if a, ok := d.pool.Get(source.id); ok && a != nil {
		if stopErr := d.Stop(ctx, source.id.String()); stopErr != nil {
			return false, fmt.Errorf("stop source: %w", stopErr)
		}
		if startErr := d.Start(ctx, source.id.String()); startErr != nil {
			return false, fmt.Errorf("restart source: %w", startErr)
		}
		return true, nil
	}
	return false, nil
}

// lookupLinkedAgents expands a slice of agent UUIDs into the
// richer LinkedAgentInfo shape. Missing rows (agent removed
// after the link was created — shouldn't happen given cascade,
// but defensive) surface with empty Name/Model and the status
// "removed".
func (d *Daemon) lookupLinkedAgents(ids []string) []LinkedAgentInfo {
	// d.agents access mirrors the existing read pattern (see
	// Info() / Get() etc.) — no lock today; the pool is the
	// authoritative live-state owner and writes to d.agents are
	// serialised through CreateAgent / Restore at startup.
	out := make([]LinkedAgentInfo, 0, len(ids))
	for _, id := range ids {
		u, err := uuid.Parse(id)
		if err != nil {
			continue
		}
		ma, ok := d.agents[id]
		if !ok {
			out = append(out, LinkedAgentInfo{ID: id, Status: "removed"})
			continue
		}
		status := "stopped"
		if a, alive := d.pool.Get(u); alive && a != nil {
			status = a.StatusTracker().Get().String()
		}
		out = append(out, LinkedAgentInfo{
			ID:     id,
			Name:   ma.name,
			Model:  ma.model,
			Status: status,
		})
	}
	return out
}
