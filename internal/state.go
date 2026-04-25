package internal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type persistedMount struct {
	Host        string `json:"host"`
	Target      string `json:"target"`
	Description string `json:"description,omitempty"`
}

type persistedAgent struct {
	ID        string
	Name      string
	AgentName string
	Model     string
	Runtime   string
	Tag       string
	Status    string
	CreatedAt time.Time
	Mounts    []persistedMount
}

type StateStore struct {
	db *sql.DB
}

func NewStateStore(ctx context.Context, db *sql.DB) (*StateStore, error) {
	if err := migrateState(ctx, db); err != nil {
		return nil, fmt.Errorf("running state migrations: %w", err)
	}

	return &StateStore{db: db}, nil
}

func migrateState(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS agents (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			agent_name TEXT NOT NULL,
			model TEXT NOT NULL,
			runtime TEXT NOT NULL,
			tag TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'running',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		return err
	}

	// mounts was added later; guard against ALTER failing on a second
	// run by checking PRAGMA table_info.
	return addColumnIfMissing(ctx, db, "agents", "mounts", "TEXT NOT NULL DEFAULT '[]'")
}

// addColumnIfMissing is a small helper for schema migrations that only
// add a single column. sqlite doesn't have IF NOT EXISTS for ALTER
// TABLE ADD COLUMN, so we inspect PRAGMA table_info and skip when the
// column is already present. Keeps migrations idempotent across
// daemon restarts without bumping a schema_version table.
func addColumnIfMissing(ctx context.Context, db *sql.DB, table, column, decl string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return fmt.Errorf("inspecting %s schema: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid      int
			name     string
			colType  string
			notnull  int
			dflt     sql.NullString
			isPKPart int
		)

		if scanErr := rows.Scan(&cid, &name, &colType, &notnull, &dflt, &isPKPart); scanErr != nil {
			return fmt.Errorf("scanning %s schema: %w", table, scanErr)
		}

		if name == column {
			return nil
		}
	}

	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterating %s schema: %w", table, err)
	}

	_, err = db.ExecContext(ctx,
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl),
	)

	return err
}

func (s *StateStore) SaveAgent(ctx context.Context, a persistedAgent) error {
	mounts, err := json.Marshal(a.Mounts)
	if err != nil {
		return fmt.Errorf("encoding mounts: %w", err)
	}

	if len(a.Mounts) == 0 {
		mounts = []byte("[]")
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO agents (id, name, agent_name, model, runtime, tag, status, created_at, mounts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.AgentName, a.Model, a.Runtime, a.Tag, a.Status, a.CreatedAt, string(mounts),
	)

	return err
}

func (s *StateStore) RemoveAgent(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM agents WHERE id = ?", id,
	)

	return err
}

func (s *StateStore) UpdateStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE agents SET status = ? WHERE id = ?", status, id,
	)

	return err
}

func (s *StateStore) ListAgents(ctx context.Context) ([]persistedAgent, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, name, agent_name, model, runtime, tag, status, created_at, mounts FROM agents ORDER BY created_at ASC",
	)
	if err != nil {
		return nil, fmt.Errorf("querying agents: %w", err)
	}
	defer rows.Close()

	var agents []persistedAgent

	for rows.Next() {
		var (
			a      persistedAgent
			mounts string
		)

		if err = rows.Scan(
			&a.ID, &a.Name, &a.AgentName, &a.Model, &a.Runtime,
			&a.Tag, &a.Status, &a.CreatedAt, &mounts,
		); err != nil {
			return nil, fmt.Errorf("scanning agent: %w", err)
		}

		if mounts != "" && mounts != "[]" {
			if err = json.Unmarshal([]byte(mounts), &a.Mounts); err != nil {
				return nil, fmt.Errorf("decoding mounts for %s: %w", a.ID, err)
			}
		}

		agents = append(agents, a)
	}

	return agents, rows.Err()
}
