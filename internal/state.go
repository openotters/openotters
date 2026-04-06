package internal

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type persistedAgent struct {
	ID        string
	Name      string
	AgentName string
	Model     string
	Tag       string
	Status    string
	CreatedAt time.Time
}

type StateStore struct {
	db *sql.DB
}

func NewStateStore(db *sql.DB) (*StateStore, error) {
	if err := migrateState(db); err != nil {
		return nil, fmt.Errorf("running state migrations: %w", err)
	}

	return &StateStore{db: db}, nil
}

func migrateState(db *sql.DB) error {
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS agents (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			agent_name TEXT NOT NULL,
			model TEXT NOT NULL,
			tag TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'running',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`)

	return err
}

func (s *StateStore) SaveAgent(a persistedAgent) error {
	_, err := s.db.ExecContext(context.Background(),
		`INSERT OR REPLACE INTO agents (id, name, agent_name, model, tag, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.AgentName, a.Model, a.Tag, a.Status, a.CreatedAt,
	)

	return err
}

func (s *StateStore) RemoveAgent(id string) error {
	_, err := s.db.ExecContext(context.Background(),
		"DELETE FROM agents WHERE id = ?", id,
	)

	return err
}

func (s *StateStore) UpdateStatus(id, status string) error {
	_, err := s.db.ExecContext(context.Background(),
		"UPDATE agents SET status = ? WHERE id = ?", status, id,
	)

	return err
}

func (s *StateStore) ListAgents() ([]persistedAgent, error) {
	rows, err := s.db.QueryContext(context.Background(),
		"SELECT id, name, agent_name, model, tag, status, created_at FROM agents ORDER BY created_at ASC",
	)
	if err != nil {
		return nil, fmt.Errorf("querying agents: %w", err)
	}
	defer rows.Close()

	var agents []persistedAgent

	for rows.Next() {
		var a persistedAgent

		if err = rows.Scan(&a.ID, &a.Name, &a.AgentName, &a.Model, &a.Tag, &a.Status, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning agent: %w", err)
		}

		agents = append(agents, a)
	}

	return agents, rows.Err()
}

func extractTag(ref string) string {
	if idx := strings.Index(ref, "/"); idx >= 0 {
		return ref[idx+1:]
	}

	return ref
}
