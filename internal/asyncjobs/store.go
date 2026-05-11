// Package asyncjobs holds the daemon-side persistence and lifecycle
// for async BIN jobs — the records of "run this BIN against this
// agent's spawn env." Jobs are attached to the agent only; arbitrary
// string labels (see proto's standard io.openotters.* keys) provide
// the metadata channel for relating jobs to sessions, parents,
// purposes, etc. The store here is the SQL layer; pool.go owns the
// worker / dispatcher / boot-replay logic.
//
// Standard reserved labels (set by callers, never validated by the
// daemon — see api/v1/daemon.proto for the canonical list):
//
//	io.openotters.session-id  chat session that originated the job
//	io.openotters.origin      cli|chat|agent-tool|scheduler|webhook|external
package asyncjobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Status is the row's lifecycle state. Stored as a TEXT CHECK column
// so a typo'd literal at the SQL level errors loudly.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusDone      Status = "done"
	StatusError     Status = "error"
	StatusCancelled Status = "cancelled"
	StatusOrphaned  Status = "orphaned"
)

// IsTerminal reports whether the status no longer transitions on its
// own (no further work the pool can do — the agent's seen it or will
// see it via the boot-time orphan callback).
func (s Status) IsTerminal() bool {
	switch s {
	case StatusDone, StatusError, StatusCancelled, StatusOrphaned:
		return true
	}
	return false
}

// Spec is the input shape Insert takes — everything required to
// dispatch the job. ID is generated server-side. Labels are stored
// verbatim; the daemon never validates or interprets them. See the
// proto for standard io.openotters.* keys.
type Spec struct {
	AgentID string
	Bin     string
	Args    []string
	Stdin   string
	Labels  map[string]string
}

// Job is the row's full materialised view. Args / Labels are decoded
// from their JSON envelopes on read; consumers don't see the
// encoding.
type Job struct {
	ID         string
	AgentID    string
	Bin        string
	Args       []string
	Stdin      string
	Labels     map[string]string
	Status     Status
	Handle     string
	ExitCode   sql.NullInt64
	Stdout     string
	Stderr     string
	Error      string
	CreatedAt  time.Time
	StartedAt  sql.NullTime
	FinishedAt sql.NullTime
}

// ErrNotFound is returned by Get / cancel-style methods when the
// row doesn't exist. Callers compare with errors.Is.
var ErrNotFound = errors.New("async job not found")

// Store is the SQL layer for async_jobs. Methods take ctx and bubble
// errors verbatim; mapping to gRPC status codes happens at the
// handler boundary.
type Store struct {
	db *sql.DB
}

// NewStore wraps an existing *sql.DB. Migrations live in the daemon's
// migrateState (see internal/state.go) so the same connection serves
// agents / images / async_jobs without per-package schema chatter.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Insert creates a new pending row and returns the generated job ID.
// The caller (typically the pool) then dispatches by ID.
func (s *Store) Insert(ctx context.Context, spec Spec) (string, error) {
	if spec.AgentID == "" {
		return "", errors.New("Insert: agent_id is required")
	}
	if spec.Bin == "" {
		return "", errors.New("Insert: bin is required")
	}

	args, err := json.Marshal(spec.Args)
	if err != nil {
		return "", fmt.Errorf("encoding args: %w", err)
	}
	if len(spec.Args) == 0 {
		args = []byte("[]")
	}

	labels, err := json.Marshal(spec.Labels)
	if err != nil {
		return "", fmt.Errorf("encoding labels: %w", err)
	}
	if len(spec.Labels) == 0 {
		labels = []byte("{}")
	}

	id := "job_" + uuid.NewString()

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO async_jobs (id, agent_id, bin, args_json, stdin, labels_json, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, spec.AgentID, spec.Bin, string(args), spec.Stdin, string(labels),
		string(StatusPending), time.Now().Unix(),
	)
	if err != nil {
		return "", fmt.Errorf("insert job: %w", err)
	}

	return id, nil
}

// Get returns one row by ID. ErrNotFound is the canonical
// "doesn't exist" answer.
func (s *Store) Get(ctx context.Context, id string) (*Job, error) {
	row := s.db.QueryRowContext(ctx, selectColumns+` WHERE id = ?`, id)

	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return job, nil
}

// ListFilter is the optional shape for ListAll / ListByAgent:
// status whitelist + label match. All conditions are AND-combined.
// A zero-value filter matches everything.
type ListFilter struct {
	Statuses []Status
	// Labels match: every key=value must be present. Empty map = no
	// label constraint. Compares against labels stored at submit
	// time; missing keys never match.
	Labels map[string]string
}

// ListAll returns every job newest-first, applying the optional
// filter. Used by operator-facing surfaces (gRPC ListAsyncJobs with
// no agent filter, the dashboard's /jobs page).
func (s *Store) ListAll(ctx context.Context, filter ListFilter) ([]*Job, error) {
	q := selectColumns
	where, args := whereClauses(filter, "")
	if where != "" {
		q += ` WHERE ` + where
	}
	q += ` ORDER BY created_at DESC`
	return s.queryJobs(ctx, q, args...)
}

// ListByAgent returns the agent's jobs newest-first, applying the
// optional filter.
func (s *Store) ListByAgent(ctx context.Context, agentID string, filter ListFilter) ([]*Job, error) {
	where, args := whereClauses(filter, agentID)
	q := selectColumns + ` WHERE ` + where + ` ORDER BY created_at DESC`
	return s.queryJobs(ctx, q, args...)
}

// whereClauses builds the WHERE body shared by ListAll / ListByAgent.
// agentID == "" means "no agent constraint." Label keys are sorted
// for query reproducibility (helps query plan caching and makes
// debug logs deterministic).
func whereClauses(filter ListFilter, agentID string) (string, []any) {
	var (
		conds []string
		args  []any
	)
	if agentID != "" {
		conds = append(conds, "agent_id = ?")
		args = append(args, agentID)
	}
	if len(filter.Statuses) > 0 {
		conds = append(conds, "status IN ("+placeholders(len(filter.Statuses))+")")
		for _, st := range filter.Statuses {
			args = append(args, string(st))
		}
	}
	if len(filter.Labels) > 0 {
		keys := make([]string, 0, len(filter.Labels))
		for k := range filter.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			// json_extract returns NULL when the key is missing,
			// which never compares equal to a non-NULL placeholder
			// — so missing keys correctly drop out of the result
			// without an extra IS NOT NULL guard.
			//
			// Quote the key (`$."..."`) so SQLite treats it as a
			// single property name rather than a dotted nested
			// path. Critical for our io.openotters.* labels —
			// `$.io.openotters.session-id` would walk into
			// non-existent nested objects.
			conds = append(conds, "json_extract(labels_json, ?) = ?")
			args = append(args, `$."`+k+`"`, filter.Labels[k])
		}
	}

	if len(conds) == 0 {
		return "", nil
	}
	out := conds[0]
	for _, c := range conds[1:] {
		out += " AND " + c
	}
	return out, args
}

// ListPending returns up to limit rows with status='pending', oldest
// first — used at boot to redispatch jobs that never started.
func (s *Store) ListPending(ctx context.Context, limit int) ([]*Job, error) {
	if limit <= 0 {
		limit = 100
	}
	q := selectColumns + ` WHERE status = ? ORDER BY created_at ASC LIMIT ?`
	return s.queryJobs(ctx, q, string(StatusPending), limit)
}

// ListRunning returns every row currently marked running. Boot uses
// it to identify ghost jobs from a prior process.
func (s *Store) ListRunning(ctx context.Context) ([]*Job, error) {
	q := selectColumns + ` WHERE status = ? ORDER BY started_at ASC`
	return s.queryJobs(ctx, q, string(StatusRunning))
}

// MarkRunning flips a pending row to running and records started_at +
// the executor's handle (PID for system, container ID for docker).
// Idempotent: re-marking a row that's already running with the same
// handle is a no-op.
func (s *Store) MarkRunning(ctx context.Context, id, handle string, started time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE async_jobs
		   SET status = ?, handle = ?, started_at = ?
		 WHERE id = ? AND status IN (?, ?)`,
		string(StatusRunning), handle, started.Unix(),
		id, string(StatusPending), string(StatusRunning),
	)
	return notFoundIfNoRows(res, err)
}

// SetHandle attaches an executor handle (PID or container ID) to a
// running row. Used when the handle isn't known at MarkRunning time
// (e.g. the executor returns it only after spawn). Best-effort:
// silently no-ops if the row's already terminal.
func (s *Store) SetHandle(ctx context.Context, id, handle string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE async_jobs SET handle = ? WHERE id = ? AND status = ?`,
		handle, id, string(StatusRunning),
	)
	return err
}

// AppendStdout concatenates chunk to the row's stdout column. Used
// by the pool's streaming sink so UI / CLI observers polling
// GetAsyncJob see growing logs mid-flight. Constrained to
// StatusRunning rows so a racing MarkDone (which rewrites stdout to
// the final buffer) wins consistently — terminal-status rows are
// the source of truth, and we never append to a row after it's
// been finalised. Best-effort: silently no-ops if the row already
// transitioned out of Running.
func (s *Store) AppendStdout(ctx context.Context, id string, chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}

	_, err := s.db.ExecContext(ctx,
		`UPDATE async_jobs SET stdout = stdout || ? WHERE id = ? AND status = ?`,
		string(chunk), id, string(StatusRunning),
	)

	return err
}

// AppendStderr is the stderr-column counterpart of AppendStdout.
// Same semantics: only updates while the row is StatusRunning, so
// the final MarkDone / MarkCancelled write is authoritative.
func (s *Store) AppendStderr(ctx context.Context, id string, chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}

	_, err := s.db.ExecContext(ctx,
		`UPDATE async_jobs SET stderr = stderr || ? WHERE id = ? AND status = ?`,
		string(chunk), id, string(StatusRunning),
	)

	return err
}

// MarkDone records a successful completion and the captured output.
//
// The stdout / stderr CASE expressions are a guard against the
// streaming sink racing the final write: the sink may have appended
// the final 200 ms tail of bytes between agent.Exec returning and
// this UPDATE firing. If that tail makes the row longer than the
// caller's captured buffer, we'd otherwise truncate it. Guard with
// length-based >= so the final write only OVERWRITES when the
// caller's buffer is at least as complete — never shrinks visible
// content.
func (s *Store) MarkDone(ctx context.Context, id string, exit int, stdout, stderr string, finished time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE async_jobs
		   SET status = ?, exit_code = ?,
		       stdout = CASE WHEN length(?) >= length(stdout) THEN ? ELSE stdout END,
		       stderr = CASE WHEN length(?) >= length(stderr) THEN ? ELSE stderr END,
		       finished_at = ?
		 WHERE id = ? AND status = ?`,
		string(StatusDone), exit,
		stdout, stdout,
		stderr, stderr,
		finished.Unix(),
		id, string(StatusRunning),
	)
	return notFoundIfNoRows(res, err)
}

// MarkError records a spawn / execution failure that prevented the
// BIN from producing a result.
func (s *Store) MarkError(ctx context.Context, id, errMsg string, finished time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE async_jobs
		   SET status = ?, error = ?, finished_at = ?
		 WHERE id = ? AND status IN (?, ?)`,
		string(StatusError), errMsg, finished.Unix(),
		id, string(StatusRunning), string(StatusPending),
	)
	return notFoundIfNoRows(res, err)
}

// MarkCancelled records that the user / shutdown path stopped a
// job mid-flight. Stdout/stderr captured up to the kill point land
// in the same row, guarded by the same length-based protection as
// MarkDone so a partial captured-buffer never erases content the
// streaming sink already pushed.
func (s *Store) MarkCancelled(ctx context.Context, id, stdout, stderr string, finished time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE async_jobs
		   SET status = ?,
		       stdout = CASE WHEN length(?) >= length(stdout) THEN ? ELSE stdout END,
		       stderr = CASE WHEN length(?) >= length(stderr) THEN ? ELSE stderr END,
		       finished_at = ?
		 WHERE id = ? AND status IN (?, ?)`,
		string(StatusCancelled),
		stdout, stdout,
		stderr, stderr,
		finished.Unix(),
		id, string(StatusRunning), string(StatusPending),
	)
	return notFoundIfNoRows(res, err)
}

// MarkOrphaned bulk-flips every running row for an agent (or for ALL
// agents when agentID == "") to orphaned. Used by Boot after a
// daemon crash and by the agent supervision pool when an agent
// restarts. Returns how many rows changed.
func (s *Store) MarkOrphaned(ctx context.Context, agentID string) (int64, error) {
	q := `UPDATE async_jobs SET status = ?, finished_at = ? WHERE status = ?`
	args := []any{string(StatusOrphaned), time.Now().Unix(), string(StatusRunning)}
	if agentID != "" {
		q += ` AND agent_id = ?`
		args = append(args, agentID)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("mark orphaned: %w", err)
	}
	return res.RowsAffected()
}

// DeleteByAgent removes every row for an agent. Belt-and-braces for
// hosts where SQLite FK enforcement is off; with --sqlite-foreign-key
// the cascade does this automatically.
func (s *Store) DeleteByAgent(ctx context.Context, agentID string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM async_jobs WHERE agent_id = ?`, agentID)
	if err != nil {
		return 0, fmt.Errorf("delete by agent: %w", err)
	}
	return res.RowsAffected()
}

// ─── helpers ────────────────────────────────────────────────────────

const selectColumns = `
	SELECT id, agent_id, bin, args_json, stdin, labels_json, status, handle,
	       exit_code, stdout, stderr, error, created_at, started_at, finished_at
	  FROM async_jobs`

// rowOrRows is the minimal interface satisfied by both *sql.Row and
// *sql.Rows for a single-row scan.
type rowOrRows interface {
	Scan(dest ...any) error
}

func scanJob(r rowOrRows) (*Job, error) {
	var (
		j           Job
		argsJSON    string
		labelsJSON  string
		statusStr   string
		createdUnix int64
		startedUnix sql.NullInt64
		finishUnix  sql.NullInt64
	)
	err := r.Scan(
		&j.ID, &j.AgentID, &j.Bin, &argsJSON, &j.Stdin, &labelsJSON,
		&statusStr, &j.Handle, &j.ExitCode, &j.Stdout, &j.Stderr, &j.Error,
		&createdUnix, &startedUnix, &finishUnix,
	)
	if err != nil {
		return nil, err
	}
	if argsJSON != "" && argsJSON != "[]" {
		if err := json.Unmarshal([]byte(argsJSON), &j.Args); err != nil {
			return nil, fmt.Errorf("decoding args for %s: %w", j.ID, err)
		}
	}
	if labelsJSON != "" && labelsJSON != "{}" {
		if err := json.Unmarshal([]byte(labelsJSON), &j.Labels); err != nil {
			return nil, fmt.Errorf("decoding labels for %s: %w", j.ID, err)
		}
	}
	j.Status = Status(statusStr)
	j.CreatedAt = time.Unix(createdUnix, 0)
	if startedUnix.Valid {
		j.StartedAt = sql.NullTime{Time: time.Unix(startedUnix.Int64, 0), Valid: true}
	}
	if finishUnix.Valid {
		j.FinishedAt = sql.NullTime{Time: time.Unix(finishUnix.Int64, 0), Valid: true}
	}
	return &j, nil
}

func (s *Store) queryJobs(ctx context.Context, query string, args ...any) ([]*Job, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying jobs: %w", err)
	}
	defer rows.Close()

	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func placeholders(n int) string {
	if n == 0 {
		return ""
	}
	out := make([]byte, 0, n*2-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '?')
	}
	return string(out)
}

// notFoundIfNoRows turns a UPDATE-affected-zero-rows result into
// ErrNotFound. Used by every Mark* method so callers can distinguish
// "row doesn't exist" from "I tried to mark something that's already
// in the wrong state."
func notFoundIfNoRows(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
