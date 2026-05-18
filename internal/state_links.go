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

// AgentLinksAdd records a directed edge source → target. INSERT
// OR IGNORE so re-adding an existing link is a silent no-op
// (callers can call without probing first).
func (s *StateStore) AgentLinksAdd(ctx context.Context, sourceID, targetID string) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO agent_links (source_agent_id, target_agent_id)
		VALUES (?, ?)`,
		sourceID, targetID,
	); err != nil {
		return fmt.Errorf("add agent link %s → %s: %w", sourceID, targetID, err)
	}
	return nil
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
