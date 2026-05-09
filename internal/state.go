package internal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
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
	if err := addColumnIfMissing(ctx, db, "agents", "mounts", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}

	// image_kinds owns the mapping from manifest digest → openotters
	// artifact kind ("application/vnd.openotters.{agent,bin}.v1").
	// Populated at ingestion time (build / pull / save) where the
	// kind is already known, so the listing path doesn't have to
	// re-derive it from each backend's idiosyncratic config layout
	// (docker doesn't surface `Config.Labels` for our agent
	// manifests; the embedded HTTP registry on system always does).
	// Keyed by digest, content-addressed, so re-tags share rows and
	// stale entries for absent digests are harmless.
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS image_kinds (
			digest        TEXT PRIMARY KEY,
			artifact_type TEXT NOT NULL,
			indexed_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		return err
	}

	return nil
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

// IndexImageKind records the openotters artifact kind for a manifest
// digest. Idempotent: re-indexing the same digest is a no-op
// (INSERT OR REPLACE updates indexed_at). Empty artifactType is
// rejected — callers should skip the call rather than persist a
// blank row.
func (s *StateStore) IndexImageKind(ctx context.Context, digest, artifactType string) error {
	if digest == "" || artifactType == "" {
		return fmt.Errorf("index image kind: digest and artifactType required")
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO image_kinds (digest, artifact_type, indexed_at)
		 VALUES (?, ?, CURRENT_TIMESTAMP)`,
		digest, artifactType,
	)

	return err
}

// GetImageKinds bulk-fetches kind rows for a set of digests.
// Returns a map keyed by digest containing only the digests
// present in the index — missing digests are absent from the map.
// Single SQL round-trip; the listing path uses this to populate
// every image's artifactType column without per-ref backend reads.
func (s *StateStore) GetImageKinds(ctx context.Context, digests []string) (map[string]string, error) {
	if len(digests) == 0 {
		return map[string]string{}, nil
	}

	placeholders := make([]string, len(digests))
	args := make([]any, len(digests))

	for i, d := range digests {
		placeholders[i] = "?"
		args[i] = d
	}

	// G202 false positive: only placeholder characters concatenated,
	// every input value is bound through args.
	//nolint:gosec // only "?" placeholders concatenated; values bound via args
	query := "SELECT digest, artifact_type FROM image_kinds WHERE digest IN (" +
		strings.Join(placeholders, ",") + ")"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying image kinds: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string, len(digests))

	for rows.Next() {
		var digest, kind string
		if scanErr := rows.Scan(&digest, &kind); scanErr != nil {
			return nil, fmt.Errorf("scanning image kind: %w", scanErr)
		}

		out[digest] = kind
	}

	return out, rows.Err()
}

// DeleteImageKind drops the index row for a digest. Used after
// RemoveImage so the index doesn't accumulate orphan rows for
// deleted content. Misses are not errors — the row may already be
// gone if a parallel RemoveImage on a different tag won the race.
func (s *StateStore) DeleteImageKind(ctx context.Context, digest string) error {
	if digest == "" {
		return nil
	}

	_, err := s.db.ExecContext(ctx,
		"DELETE FROM image_kinds WHERE digest = ?", digest,
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
