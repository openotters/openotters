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
	ReadOnly    bool   `json:"read_only,omitempty"`
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

	// images is the daemon-owned cache of every ref the executor
	// registry has shown us. Populated at every ingestion site
	// (build / pull / save) and kept in sync via RefreshImages
	// (walks the executor registry on demand). ListImages reads
	// exclusively from this table — no per-call docker round
	// trip — so the dashboard's listing surfaces are SQL-fast.
	// Refs are unique per registry; refs sharing a digest get a
	// row each (matches how cli.ImageList expands RepoTags).
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS images (
			ref           TEXT PRIMARY KEY,
			digest        TEXT NOT NULL,
			artifact_type TEXT NOT NULL DEFAULT '',
			size          INTEGER NOT NULL DEFAULT 0,
			created_unix  INTEGER NOT NULL DEFAULT 0,
			description   TEXT NOT NULL DEFAULT '',
			source        TEXT NOT NULL DEFAULT '',
			indexed_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_images_digest ON images(digest)`,
	); err != nil {
		return err
	}

	// image_kinds was the previous, narrower index. Drop it on
	// upgrade — same alpha breakage policy as the agentfile
	// format change that landed earlier.
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS image_kinds`); err != nil {
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

// PersistedImage is the on-disk row shape — one per ref. The
// daemon's ListImages serves these directly; build / pull / save
// flows upsert through UpsertImage when they have a fresh fact.
type PersistedImage struct {
	Ref          string
	Digest       string
	ArtifactType string
	Size         int64
	CreatedUnix  int64
	Description  string
	Source       string
}

// UpsertImage records (or refreshes) one image row. Idempotent —
// INSERT OR REPLACE keyed by ref so a re-tag of the same digest
// updates the same row, and a re-pull with new metadata wins.
func (s *StateStore) UpsertImage(ctx context.Context, img PersistedImage) error {
	if img.Ref == "" {
		return fmt.Errorf("upsert image: ref required")
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO images
		 (ref, digest, artifact_type, size, created_unix, description, source, indexed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		img.Ref, img.Digest, img.ArtifactType, img.Size, img.CreatedUnix,
		img.Description, img.Source,
	)

	return err
}

// ListImages returns every row in insertion-stable order. The
// dashboard's image listing surfaces serve directly from this — no
// docker / embedded-registry round trip per call.
func (s *StateStore) ListImages(ctx context.Context) ([]PersistedImage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ref, digest, artifact_type, size, created_unix, description, source
		 FROM images ORDER BY ref ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying images: %w", err)
	}
	defer rows.Close()

	var out []PersistedImage

	for rows.Next() {
		var img PersistedImage
		if scanErr := rows.Scan(
			&img.Ref, &img.Digest, &img.ArtifactType, &img.Size,
			&img.CreatedUnix, &img.Description, &img.Source,
		); scanErr != nil {
			return nil, fmt.Errorf("scanning image: %w", scanErr)
		}

		out = append(out, img)
	}

	return out, rows.Err()
}

// DeleteImageByRef drops a single ref. Used when the operator
// explicitly removes a tag without nuking the underlying digest
// (other refs may still alias it).
func (s *StateStore) DeleteImageByRef(ctx context.Context, ref string) error {
	if ref == "" {
		return nil
	}

	_, err := s.db.ExecContext(ctx, "DELETE FROM images WHERE ref = ?", ref)

	return err
}

// DeleteImagesByDigest drops every row sharing a digest. The
// docker executor's ContainerRemove(force) untags every alias
// when removing one, so the daemon mirrors that semantic — the DB
// loses every ref that shared the deleted content.
func (s *StateStore) DeleteImagesByDigest(ctx context.Context, digest string) error {
	if digest == "" {
		return nil
	}

	_, err := s.db.ExecContext(ctx, "DELETE FROM images WHERE digest = ?", digest)

	return err
}

// ReplaceAllImages reconciles the table against the current
// authoritative set in one transaction: every supplied row is
// upserted; rows whose ref isn't present any more are deleted.
// Used by RefreshImages to bring the DB into agreement with the
// executor registry's ListEntries result.
func (s *StateStore) ReplaceAllImages(ctx context.Context, imgs []PersistedImage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	if _, err = tx.ExecContext(ctx, "DELETE FROM images"); err != nil {
		return fmt.Errorf("clearing images: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO images
		 (ref, digest, artifact_type, size, created_unix, description, source, indexed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
	)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}

	defer stmt.Close()

	for _, img := range imgs {
		if img.Ref == "" {
			continue
		}

		if _, err = stmt.ExecContext(ctx,
			img.Ref, img.Digest, img.ArtifactType, img.Size, img.CreatedUnix,
			img.Description, img.Source,
		); err != nil {
			return fmt.Errorf("inserting %s: %w", img.Ref, err)
		}
	}

	return tx.Commit()
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
