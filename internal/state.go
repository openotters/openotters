package internal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type persistedMount struct {
	Host        string `json:"host"`
	Target      string `json:"target"`
	Description string `json:"description,omitempty"`
	ReadOnly    bool   `json:"read_only,omitempty"`
}

// persistedEnv is the on-disk shape of a per-run ENV override. The
// agent's Agentfile defines the schema (key + description); the
// operator's `otters run -e KEY=VAL` supplies the value. Persisting
// is what makes the override survive a daemon restart.
type persistedEnv struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type persistedAgent struct {
	ID        string
	Name      string
	AgentName string
	Model     string
	Runtime   string
	Tag       string
	Status    string
	// FailureReason narrows Status=="failed" to a specific cause
	// (pull / init / model / readiness_timeout / crashed). Empty for
	// non-failed rows.
	FailureReason string
	CreatedAt     time.Time
	Mounts        []persistedMount
	// Envs are the per-run ENV overrides supplied at CreateAgent
	// (CLI `-e KEY=VAL` / dashboard run-from-image dialog). Stored
	// as JSON in the agents.envs_json column; re-applied on Restore
	// so daemon restarts don't lose operator-supplied values.
	Envs []persistedEnv
	// Labels — see api/v1/daemon.proto's "labels (shared semantics)"
	// comment for the reserved io.openotters.* keys. Stored as JSON
	// in the agents.labels_json column.
	Labels map[string]string
	// Token + TokenJTI — JWT minted at CreateAgent and persisted so
	// the runtime keeps the same credential across daemon restarts.
	// TokenJTI is the JWT's `jti` claim (extracted at issuance), used
	// by RemoveAgent to revoke the token via state.RevokeToken.
	// Empty for v1-era rows; daemon.Restore re-tokenizes on first
	// boot under this schema.
	Token    string
	TokenJTI string
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

	// labels_json — arbitrary user-supplied key=value metadata
	// attached at CreateAgent time. Filterable via ListAgents'
	// label_selector field. See api/v1/daemon.proto for the
	// reserved io.openotters.* keys.
	if err := addColumnIfMissing(ctx, db, "agents", "labels_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return err
	}

	// token + token_jti — per-agent JWT minted at CreateAgent so the
	// runtime can call back into the daemon over the authenticated
	// TCP endpoint. token is the signed string injected as
	// OTTERS_AGENT_TOKEN; token_jti is the random claim id used to
	// revoke the token (entry in revoked_tokens) when the agent is
	// removed. Both default empty for v1-era rows; daemon.Restore
	// re-tokenizes them on first boot under this version.
	if err := addColumnIfMissing(ctx, db, "agents", "token", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(ctx, db, "agents", "token_jti", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}

	// envs_json carries per-run ENV overrides supplied at CreateAgent
	// (the CLI's `-e KEY=VAL` flag, the run-from-image dialog) so
	// they survive daemon restarts. Empty JSON array for agents that
	// took no overrides. Pre-existing rows default to "[]" via the
	// addColumnIfMissing default.
	if err := addColumnIfMissing(ctx, db, "agents",
		"envs_json", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}

	// failure_reason narrows status='failed' to a specific cause
	// (pull / init / model / readiness_timeout / crashed). Empty for
	// non-failed rows. Replaces the older 'init_error' / 'pull_error'
	// / 'model_error' status strings — the migration below remaps
	// legacy rows in place.
	if err := addColumnIfMissing(ctx, db, "agents",
		"failure_reason", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}

	if err := migrateLegacyAgentStatus(ctx, db); err != nil {
		return err
	}

	if err := migrateImagesTable(ctx, db); err != nil {
		return err
	}

	if err := migrateAsyncJobsTable(ctx, db); err != nil {
		return err
	}

	if err := migrateAuthTables(ctx, db); err != nil {
		return err
	}

	if err := migrateAgentStateTables(ctx, db); err != nil {
		return err
	}

	if err := migrateAgentLinksTable(ctx, db); err != nil {
		return err
	}

	return nil
}

// migrateAgentLinksTable owns the directed-edge table the
// agent-to-agent linking feature reads. Every row is "source can
// call target"; the reverse direction is a separate row, by
// design — asymmetric topologies (orchestrator → workers without
// the inverse) are first-class.
//
// RemoveAgent cascades both source and target columns so a
// removed agent can't haunt the graph as either side of an edge.
func migrateAgentLinksTable(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS agent_links (
			source_agent_id TEXT NOT NULL,
			target_agent_id TEXT NOT NULL,
			created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (source_agent_id, target_agent_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_links_target
			ON agent_links(target_agent_id)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("agent_links schema: %w", err)
		}
	}
	return nil
}

// migrateAgentStateTables owns the per-agent persistent state the
// runtime used to keep in its own per-agent sqlite file. Moving it
// here lets the daemon serve the operator UI directly (no per-agent
// gRPC proxy hop) and lets the per-agent FHS stay read-mostly
// (zero writeable sqlite inside the agent's root once the runtime
// switches to the daemon-backed store).
//
// Both tables are scoped by agent_id; RemoveAgent cascades DELETEs
// in this file's runtime — see deleteAgent below.
func migrateAgentStateTables(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		// agent_messages — chat history + branches, replacing the
		// runtime's per-agent messages table. id is daemon-owned
		// AUTOINCREMENT so messages are globally orderable; the
		// composite lookup index by (agent_id, session_id, id) drives
		// every GetMessages / ListMessages read.
		`CREATE TABLE IF NOT EXISTS agent_messages (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id      TEXT NOT NULL,
			session_id    TEXT NOT NULL,
			role          TEXT NOT NULL,
			content       TEXT NOT NULL,
			branches_json TEXT NOT NULL DEFAULT '[]',
			active_branch INTEGER NOT NULL DEFAULT 0,
			created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_messages_lookup
			ON agent_messages(agent_id, session_id, id)`,

		// agent_notes — the per-agent durable KV. Primary key is
		// (agent_id, key) so a key is unique per agent but reusable
		// across agents (every agent gets its own "k8s-cluster"
		// note independent of another's).
		`CREATE TABLE IF NOT EXISTS agent_notes (
			agent_id   TEXT NOT NULL,
			key        TEXT NOT NULL,
			content    TEXT NOT NULL,
			preview    TEXT NOT NULL DEFAULT '',
			in_context INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (agent_id, key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_notes_updated
			ON agent_notes(agent_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_notes_in_context
			ON agent_notes(agent_id, in_context) WHERE in_context = 1`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("agent state schema: %w", err)
		}
	}
	return nil
}

// migrateImagesTable owns the images describe-cache + the
// dropped-on-upgrade image_kinds table. Extracted from migrateState
// to keep that function under the funlen bar.
func migrateImagesTable(ctx context.Context, db *sql.DB) error {
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
			-- Describe-time fields cached at ingest so DescribeImage
			-- is a single SQL read instead of an ImageSave round-trip
			-- against docker (multi-MB) per call. Each is JSON-encoded
			-- where the upstream shape is structured; empty string
			-- means "the executor couldn't surface this" rather than
			-- "no data" so the dashboard can distinguish gracefully.
			config_json   TEXT NOT NULL DEFAULT '',
			labels_json   TEXT NOT NULL DEFAULT '{}',
			layers_json   TEXT NOT NULL DEFAULT '[]',
			indexed_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		return err
	}

	// Idempotent ADD COLUMN for daemons whose images table pre-dates
	// the describe cache.
	for _, col := range []struct {
		name string
		ddl  string
	}{
		{"config_json", "TEXT NOT NULL DEFAULT ''"},
		{"labels_json", "TEXT NOT NULL DEFAULT '{}'"},
		{"layers_json", "TEXT NOT NULL DEFAULT '[]'"},
	} {
		if err := addColumnIfMissing(ctx, db, "images", col.name, col.ddl); err != nil {
			return err
		}
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

// migrateAsyncJobsTable owns the async_jobs table + the legacy
// sessions index it depends on. Extracted from migrateState.
func migrateAsyncJobsTable(ctx context.Context, db *sql.DB) error {
	// async_jobs is the daemon-owned record of every async BIN call
	// dispatched against an agent's spawn env. The pool reads /
	// writes status lines here; the boot path replays pending and
	// orphans abandoned-running rows. Cascade keeps the table
	// honest when an agent is removed — provided the daemon is run
	// with --sqlite-foreign-key.
	//
	// Jobs are attached to the agent only — there is no session_id
	// column. Submitters that want to associate a job with a chat
	// session set the io.openotters.session-id label, and the same
	// label-selector path on ListAsyncJobs filters on it.
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS async_jobs (
			id            TEXT PRIMARY KEY,
			agent_id      TEXT NOT NULL,
			bin           TEXT NOT NULL,
			args_json     TEXT NOT NULL DEFAULT '[]',
			stdin         TEXT NOT NULL DEFAULT '',
			labels_json   TEXT NOT NULL DEFAULT '{}',
			status        TEXT NOT NULL CHECK (status IN
			                ('pending','running','done','error','cancelled','orphaned')),
			handle        TEXT NOT NULL DEFAULT '',
			exit_code     INTEGER,
			stdout        TEXT NOT NULL DEFAULT '',
			stderr        TEXT NOT NULL DEFAULT '',
			error         TEXT NOT NULL DEFAULT '',
			created_at    INTEGER NOT NULL,
			started_at    INTEGER,
			finished_at   INTEGER,
			FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
		);
	`); err != nil {
		return err
	}

	if err := addColumnIfMissing(ctx, db,
		"async_jobs", "labels_json", "TEXT NOT NULL DEFAULT '{}'",
	); err != nil {
		return err
	}

	// Idempotent DROP COLUMN for daemons whose async_jobs table
	// pre-dates the session-detachment refactor.
	if err := dropColumnIfPresent(ctx, db, "async_jobs", "session_id"); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_async_jobs_agent ON async_jobs(agent_id, status)`,
	); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_async_jobs_status ON async_jobs(status, created_at)`,
	); err != nil {
		return err
	}

	// The sessions index table existed solely to resolve session-id
	// → agent-id at SubmitAsyncJob time. With jobs now attached to
	// agents directly, the table has no other callers.
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS sessions`); err != nil {
		return err
	}

	return nil
}

// migrateAuthTables owns the secrets keystore + revoked_tokens
// list. Extracted from migrateState.
func migrateAuthTables(ctx context.Context, db *sql.DB) error {
	// secrets is the daemon's keystore. Single-row use today
	// (jwt_signing_key); kept generic so future per-secret rows
	// (rotation keys, OIDC client secrets) slot in without another
	// table. Value is a BLOB so binary keys round-trip cleanly.
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS secrets (
			name        TEXT PRIMARY KEY,
			value       BLOB NOT NULL,
			created_at  INTEGER NOT NULL
		)
	`); err != nil {
		return err
	}

	// revoked_tokens is the JWT revocation list. JWTs are
	// stateless-by-design; the only way to invalidate one before
	// exp is to remember its jti and check on every validate.
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS revoked_tokens (
			jti         TEXT PRIMARY KEY,
			revoked_at  INTEGER NOT NULL,
			reason      TEXT NOT NULL DEFAULT ''
		)
	`); err != nil {
		return err
	}

	return nil
}

// PutSecret upserts a named secret. Used by auth.LoadOrCreateSigningKey
// to persist the JWT signing key on first boot. Caller responsible for
// not logging the value.
func (s *StateStore) PutSecret(ctx context.Context, name string, value []byte) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO secrets (name, value, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET value = excluded.value`,
		name, value, time.Now().Unix(),
	)
	return err
}

// GetSecret reads a named secret. Returns (nil, nil) when the row
// is absent — caller distinguishes "not yet generated" from a real
// I/O error.
func (s *StateStore) GetSecret(ctx context.Context, name string) ([]byte, error) {
	var value []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM secrets WHERE name = ?`, name,
	).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return value, err
}

// RevokeToken adds a jti to the revocation list. Idempotent: re-revoking
// an already-revoked jti is a no-op (the original revoked_at + reason
// stay).
func (s *StateStore) RevokeToken(ctx context.Context, jti, reason string) error {
	if jti == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO revoked_tokens (jti, revoked_at, reason) VALUES (?, ?, ?)
		ON CONFLICT(jti) DO NOTHING`,
		jti, time.Now().Unix(), reason,
	)
	return err
}

// IsRevoked reports whether jti has been revoked. Used by the JWT
// validator on every TCP request — keep cheap.
func (s *StateStore) IsRevoked(ctx context.Context, jti string) (bool, error) {
	if jti == "" {
		return false, nil
	}
	var found int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM revoked_tokens WHERE jti = ?`, jti,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return found == 1, nil
}

// dropColumnIfPresent inverts addColumnIfMissing: looks for `column`
// in `table` via PRAGMA table_info, and runs ALTER TABLE DROP COLUMN
// only when it's there. SQLite's DROP COLUMN has been supported
// since 3.35; modernc.org/sqlite tracks recent versions, so this
// works without the temp-table dance. Used for forward-only schema
// cleanups like the session_id removal — fresh installs never had
// the column, upgrades shed it on first boot.
func dropColumnIfPresent(ctx context.Context, db *sql.DB, table, column string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return fmt.Errorf("inspecting %s schema: %w", table, err)
	}
	defer rows.Close()

	found := false
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
			found = true
			break
		}
	}

	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterating %s schema: %w", table, err)
	}

	if !found {
		return nil
	}

	_, err = db.ExecContext(ctx,
		fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", table, column),
	)
	return err
}

// migrateLegacyAgentStatus rewrites legacy status strings the
// executor enum no longer uses ({created, running, init_error,
// pull_error, model_error} → {starting, ready, failed+reason}).
// Each UPDATE only matches the legacy value it owns, so the function
// is idempotent — re-running it after upgrade is a no-op.
//
// Extracted from migrateState so the latter stays under the funlen
// 100-line bar.
func migrateLegacyAgentStatus(ctx context.Context, db *sql.DB) error {
	mappings := []struct {
		desc string
		sql  string
	}{
		{"pull_error", `UPDATE agents SET failure_reason='pull',  status='failed' WHERE status='pull_error'`},
		{"init_error", `UPDATE agents SET failure_reason='init',  status='failed' WHERE status='init_error'`},
		{"model_error", `UPDATE agents SET failure_reason='model', status='failed' WHERE status='model_error'`},
		{"created", `UPDATE agents SET status='starting' WHERE status='created'`},
		{"running", `UPDATE agents SET status='ready'    WHERE status='running'`},
	}
	for _, m := range mappings {
		if _, err := db.ExecContext(ctx, m.sql); err != nil {
			return fmt.Errorf("migrate %s status: %w", m.desc, err)
		}
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

	labels, err := json.Marshal(a.Labels)
	if err != nil {
		return fmt.Errorf("encoding labels: %w", err)
	}

	if len(a.Labels) == 0 {
		labels = []byte("{}")
	}

	envs, err := json.Marshal(a.Envs)
	if err != nil {
		return fmt.Errorf("encoding envs: %w", err)
	}

	if len(a.Envs) == 0 {
		envs = []byte("[]")
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO agents
		   (id, name, agent_name, model, runtime, tag, status, failure_reason,
		    created_at, mounts, labels_json, envs_json, token, token_jti)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.AgentName, a.Model, a.Runtime, a.Tag, a.Status,
		a.FailureReason, a.CreatedAt, string(mounts), string(labels),
		string(envs), a.Token, a.TokenJTI,
	)

	return err
}

func (s *StateStore) RemoveAgent(ctx context.Context, id string) error {
	// Cascade the agent's persistent state (messages + notes) so
	// agent_id collisions can't surface stale rows under a new
	// UUID. Best-effort: log-and-continue on the secondary deletes;
	// the primary DELETE FROM agents is the source of truth for
	// "agent exists".
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM agent_messages WHERE agent_id = ?", id,
	); err != nil {
		return fmt.Errorf("cascade agent_messages: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM agent_notes WHERE agent_id = ?", id,
	); err != nil {
		return fmt.Errorf("cascade agent_notes: %w", err)
	}
	// Cascade both ends of the link graph: a removed agent
	// disappears as both source and target.
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM agent_links WHERE source_agent_id = ? OR target_agent_id = ?", id, id,
	); err != nil {
		return fmt.Errorf("cascade agent_links: %w", err)
	}

	_, err := s.db.ExecContext(ctx,
		"DELETE FROM agents WHERE id = ?", id,
	)

	return err
}

// UpdateAgentToken rotates the token + jti columns for one agent.
// Used by the link-mutation path: when an agent's outbound link
// set changes, the daemon re-issues the JWT and persists the new
// token here so the next Start picks it up.
func (s *StateStore) UpdateAgentToken(ctx context.Context, id, token, jti string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET token = ?, token_jti = ? WHERE id = ?`,
		token, jti, id,
	)
	return err
}

// UpdateStatus persists the agent's status string. Pair with
// UpdateFailureReason when flipping to / from "failed" so the row's
// failure_reason matches the new status.
func (s *StateStore) UpdateStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE agents SET status = ? WHERE id = ?", status, id,
	)

	return err
}

// UpdateFailureReason persists the failure_reason column. The empty
// string clears it (used when moving out of failed back to a healthy
// state).
func (s *StateStore) UpdateFailureReason(ctx context.Context, id, reason string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE agents SET failure_reason = ? WHERE id = ?", reason, id,
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
	// Describe-time cache: filled at ingest so DescribeImage doesn't
	// re-do ImageSave / fetch-manifest work per call. Empty values
	// are valid ("we couldn't surface this for this image") and the
	// DescribeImage handler falls through to a live fetch if it
	// needs richer data than what's cached.
	ConfigJSON string
	LabelsJSON string
	LayersJSON string
}

// UpsertImage records (or refreshes) one image row. Idempotent —
// INSERT OR REPLACE keyed by ref so a re-tag of the same digest
// updates the same row, and a re-pull with new metadata wins.
func (s *StateStore) UpsertImage(ctx context.Context, img PersistedImage) error {
	if img.Ref == "" {
		return fmt.Errorf("upsert image: ref required")
	}

	labels := img.LabelsJSON
	if labels == "" {
		labels = "{}"
	}

	layers := img.LayersJSON
	if layers == "" {
		layers = "[]"
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO images
		 (ref, digest, artifact_type, size, created_unix, description, source,
		  config_json, labels_json, layers_json, indexed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		img.Ref, img.Digest, img.ArtifactType, img.Size, img.CreatedUnix,
		img.Description, img.Source,
		img.ConfigJSON, labels, layers,
	)

	return err
}

// ListImages returns every row in insertion-stable order. The
// dashboard's image listing surfaces serve directly from this — no
// docker / embedded-registry round trip per call.
func (s *StateStore) ListImages(ctx context.Context) ([]PersistedImage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ref, digest, artifact_type, size, created_unix, description, source,
		        config_json, labels_json, layers_json
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
			&img.ConfigJSON, &img.LabelsJSON, &img.LayersJSON,
		); scanErr != nil {
			return nil, fmt.Errorf("scanning image: %w", scanErr)
		}

		out = append(out, img)
	}

	return out, rows.Err()
}

// GetImage looks up a single row by ref. Returns (nil, nil) when
// the row is absent — the dashboard's describe path uses this as
// "cache miss, fall back to live fetch" rather than a hard error.
func (s *StateStore) GetImage(ctx context.Context, ref string) (*PersistedImage, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT ref, digest, artifact_type, size, created_unix, description, source,
		        config_json, labels_json, layers_json
		 FROM images WHERE ref = ?`,
		ref,
	)

	var img PersistedImage
	err := row.Scan(
		&img.Ref, &img.Digest, &img.ArtifactType, &img.Size,
		&img.CreatedUnix, &img.Description, &img.Source,
		&img.ConfigJSON, &img.LabelsJSON, &img.LayersJSON,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // documented miss sentinel
	}

	if err != nil {
		return nil, fmt.Errorf("querying image %s: %w", ref, err)
	}

	return &img, nil
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
		`SELECT id, name, agent_name, model, runtime, tag, status, failure_reason,
		        created_at, mounts, labels_json, envs_json, token, token_jti
		 FROM agents ORDER BY created_at ASC`,
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
			labels string
			envs   string
		)

		if err = rows.Scan(
			&a.ID, &a.Name, &a.AgentName, &a.Model, &a.Runtime,
			&a.Tag, &a.Status, &a.FailureReason, &a.CreatedAt, &mounts, &labels,
			&envs, &a.Token, &a.TokenJTI,
		); err != nil {
			return nil, fmt.Errorf("scanning agent: %w", err)
		}

		if mounts != "" && mounts != "[]" {
			if err = json.Unmarshal([]byte(mounts), &a.Mounts); err != nil {
				return nil, fmt.Errorf("decoding mounts for %s: %w", a.ID, err)
			}
		}
		if labels != "" && labels != "{}" {
			if err = json.Unmarshal([]byte(labels), &a.Labels); err != nil {
				return nil, fmt.Errorf("decoding labels for %s: %w", a.ID, err)
			}
		}
		if envs != "" && envs != "[]" {
			if err = json.Unmarshal([]byte(envs), &a.Envs); err != nil {
				return nil, fmt.Errorf("decoding envs for %s: %w", a.ID, err)
			}
		}

		agents = append(agents, a)
	}

	return agents, rows.Err()
}
