package internal

// state_links.go owns the per-agent directed-link graph stored in
// the agent_links table. Each row is "source can call target" —
// asymmetric by design. The cascade in RemoveAgent (state.go)
// wipes both columns when an agent is removed.
//
// The daemon's Link / Unlink handlers wrap these methods, then
// re-issue the source's JWT (so the new link set is authoritative
// in the token) and bounce the running agent so the runtime picks
// up the refreshed credential.

import (
	"context"
	"fmt"
)

// AgentLinksAdd records a directed edge source → target with an
// optional operator-supplied description. When description is
// non-empty on a re-add, it overwrites the previous value (so
// `otters link a b --description "..."` lets the operator update
// the rationale without unlink+link). Empty description preserves
// whatever was there.
func (s *StateStore) AgentLinksAdd(ctx context.Context, sourceID, targetID, description string) error {
	if description == "" {
		// INSERT OR IGNORE — re-adding a known edge is a no-op
		// so callers don't need to probe first.
		if _, err := s.db.ExecContext(ctx, `
			INSERT OR IGNORE INTO agent_links (source_agent_id, target_agent_id)
			VALUES (?, ?)`,
			sourceID, targetID,
		); err != nil {
			return fmt.Errorf("add agent link %s → %s: %w", sourceID, targetID, err)
		}
		return nil
	}
	// Description supplied → upsert so a re-add with a new
	// description rewrites the row in place.
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_links (source_agent_id, target_agent_id, description)
		VALUES (?, ?, ?)
		ON CONFLICT(source_agent_id, target_agent_id) DO UPDATE SET
		  description = excluded.description`,
		sourceID, targetID, description,
	); err != nil {
		return fmt.Errorf("add agent link %s → %s: %w", sourceID, targetID, err)
	}
	return nil
}

// AgentLinkDetail bundles one outbound edge with its operator-
// supplied description (empty when none was set; caller falls
// back to the target's labels["description"]).
type AgentLinkDetail struct {
	TargetID    string
	Description string
}

// AgentLinksListOutboundDetails returns the source's outbound
// edges with their per-link descriptions. Used by ListAgentLinks
// (operator UI) + AgentList (agent-facing) — both surfaces show
// the effective description, with the daemon falling back to the
// target's labels when the override is empty.
func (s *StateStore) AgentLinksListOutboundDetails(
	ctx context.Context, sourceID string,
) ([]AgentLinkDetail, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT target_agent_id, description
		  FROM agent_links
		 WHERE source_agent_id = ?
		 ORDER BY created_at`,
		sourceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list outbound link details: %w", err)
	}
	defer rows.Close()

	var out []AgentLinkDetail
	for rows.Next() {
		var d AgentLinkDetail
		if scanErr := rows.Scan(&d.TargetID, &d.Description); scanErr != nil {
			return nil, fmt.Errorf("scan outbound link detail: %w", scanErr)
		}
		out = append(out, d)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate outbound link details: %w", rowsErr)
	}
	return out, nil
}

// AgentInboundLinkDetail bundles one inbound edge with its
// description (the source-side rationale for the link). Mirror of
// AgentLinkDetail but with the field name flipped from
// TargetID to SourceID.
type AgentInboundLinkDetail struct {
	SourceID    string
	Description string
}

// AgentLinksListInboundDetails returns the inbound edges + per-
// link descriptions targeted at the supplied id. Mirror of the
// outbound query; used by the operator UI's "linked from" view.
func (s *StateStore) AgentLinksListInboundDetails(
	ctx context.Context, targetID string,
) ([]AgentInboundLinkDetail, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT source_agent_id, description
		  FROM agent_links
		 WHERE target_agent_id = ?
		 ORDER BY created_at`,
		targetID,
	)
	if err != nil {
		return nil, fmt.Errorf("list inbound link details: %w", err)
	}
	defer rows.Close()

	var out []AgentInboundLinkDetail
	for rows.Next() {
		var d AgentInboundLinkDetail
		if scanErr := rows.Scan(&d.SourceID, &d.Description); scanErr != nil {
			return nil, fmt.Errorf("scan inbound link detail: %w", scanErr)
		}
		out = append(out, d)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate inbound link details: %w", rowsErr)
	}
	return out, nil
}

// AgentLinksRemove drops a single directed edge. Missing rows are
// silent successes — the operator UI doesn't distinguish "wasn't
// there" from "was there and now isn't".
func (s *StateStore) AgentLinksRemove(ctx context.Context, sourceID, targetID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM agent_links WHERE source_agent_id = ? AND target_agent_id = ?`,
		sourceID, targetID,
	); err != nil {
		return fmt.Errorf("remove agent link %s → %s: %w", sourceID, targetID, err)
	}
	return nil
}

// AgentLinksListOutbound returns the agent IDs the source can
// call. Drives JWT issuance (Claims.Links) and the agent-facing
// agent_list tool.
func (s *StateStore) AgentLinksListOutbound(ctx context.Context, sourceID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT target_agent_id FROM agent_links WHERE source_agent_id = ? ORDER BY created_at`,
		sourceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list outbound links: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, fmt.Errorf("scan outbound link: %w", scanErr)
		}
		out = append(out, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate outbound links: %w", rowsErr)
	}
	return out, nil
}

// AgentLinksListInbound returns the agent IDs that can call the
// target. Powers the UI's "linked from" view; not used by the
// agent-facing tools (no agent can enumerate who has it as a
// target through its own JWT).
func (s *StateStore) AgentLinksListInbound(ctx context.Context, targetID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source_agent_id FROM agent_links WHERE target_agent_id = ? ORDER BY created_at`,
		targetID,
	)
	if err != nil {
		return nil, fmt.Errorf("list inbound links: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, fmt.Errorf("scan inbound link: %w", scanErr)
		}
		out = append(out, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate inbound links: %w", rowsErr)
	}
	return out, nil
}
