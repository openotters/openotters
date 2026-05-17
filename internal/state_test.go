// the StateStore's db field are unexported; the daemon exposes a
// higher-level RemoveAgent but the cascade invariant is easier to
// pin by inspecting the underlying tables directly.
//
//nolint:testpackage // White-box test: migrateAgentStateTables and
package internal

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// openTestState opens a fresh sqlite db in t.TempDir, runs the
// daemon's migrations against it, and returns a StateStore. Cheap
// enough to call per-test.
func openTestState(t *testing.T) *StateStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "daemon.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewStateStore(context.Background(), db)
	if err != nil {
		t.Fatalf("NewStateStore: %v", err)
	}
	return store
}

// TestRemoveAgent_CascadesPerAgentState pins the invariant the
// state-migration backbone leans on: deleting an agent wipes its
// rows in agent_messages + agent_notes. Without this, a future
// agent reusing the same UUID would see another agent's state —
// the load-bearing reason both tables sit in the daemon's db at
// all.
func TestRemoveAgent_CascadesPerAgentState(t *testing.T) {
	t.Parallel()
	store := openTestState(t)
	ctx := context.Background()

	const agentA = "agent-a"
	const agentB = "agent-b"

	// Seed both agents with state, plus rows for an unrelated
	// agent we DON'T delete (B) — the cascade must be scoped.
	for _, id := range []string{agentA, agentB} {
		if _, err := store.db.ExecContext(ctx,
			`INSERT INTO agent_messages (agent_id, session_id, role, content) VALUES (?, ?, 'user', 'hi')`,
			id, "session-1",
		); err != nil {
			t.Fatalf("seed message for %s: %v", id, err)
		}
		if _, err := store.db.ExecContext(ctx,
			`INSERT INTO agent_notes (agent_id, key, content) VALUES (?, ?, ?)`,
			id, "k", "v",
		); err != nil {
			t.Fatalf("seed note for %s: %v", id, err)
		}
		if _, err := store.db.ExecContext(ctx,
			`INSERT INTO agents (id, name, agent_name, model, runtime, tag) VALUES (?, ?, ?, ?, ?, ?)`,
			id, "name-"+id, "x", "m", "r", "t",
		); err != nil {
			t.Fatalf("seed agent row for %s: %v", id, err)
		}
	}

	if err := store.RemoveAgent(ctx, agentA); err != nil {
		t.Fatalf("RemoveAgent: %v", err)
	}

	// agent A's rows must be gone.
	if n := countRows(t, store, "agent_messages", agentA); n != 0 {
		t.Errorf("agent_messages for A: got %d, want 0", n)
	}
	if n := countRows(t, store, "agent_notes", agentA); n != 0 {
		t.Errorf("agent_notes for A: got %d, want 0", n)
	}

	// agent B's rows must survive — the cascade is per-agent, not
	// wholesale.
	if n := countRows(t, store, "agent_messages", agentB); n != 1 {
		t.Errorf("agent_messages for B: got %d, want 1", n)
	}
	if n := countRows(t, store, "agent_notes", agentB); n != 1 {
		t.Errorf("agent_notes for B: got %d, want 1", n)
	}
}

func TestRemoveAgent_NoStateIsFine(t *testing.T) {
	t.Parallel()
	store := openTestState(t)
	ctx := context.Background()

	// Insert an agent with no per-agent state — the cascade DELETEs
	// must succeed as no-ops, not error.
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO agents (id, name, agent_name, model, runtime, tag) VALUES (?, ?, ?, ?, ?, ?)`,
		"empty", "name-empty", "x", "m", "r", "t",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.RemoveAgent(ctx, "empty"); err != nil {
		t.Fatalf("RemoveAgent on stateless agent: %v", err)
	}
}

func TestMigrateAgentStateTables_Idempotent(t *testing.T) {
	t.Parallel()
	store := openTestState(t)

	// Re-running migrations against the same db must not error —
	// the daemon re-runs migrateState on every boot.
	if err := migrateAgentStateTables(context.Background(), store.db); err != nil {
		t.Fatalf("second migrateAgentStateTables: %v", err)
	}
}

func countRows(t *testing.T, store *StateStore, table, agentID string) int {
	t.Helper()
	var n int
	err := store.db.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM "+table+" WHERE agent_id = ?", agentID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count %s for %s: %v", table, agentID, err)
	}
	return n
}
