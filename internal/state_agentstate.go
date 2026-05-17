package internal

// state_agentstate.go owns the per-agent CRUD against the
// agent_messages and agent_notes tables. Kept in its own file so
// state.go doesn't grow past readable; every method here is a
// thin wrapper over a single SQL statement scoped by agent_id.
//
// The Connect handlers in agentstate_handler.go authenticate the
// caller via the JWT (auth.AgentScopedInterceptor) and pass the
// resolved agent_id through. None of the methods below should
// ever be called with an empty agent_id — the interceptor rejects
// such calls before they reach the handler.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// AgentMessageRow mirrors agent_messages columns. Same shape the
// proto MessageRow carries on the wire; the handlers translate.
type AgentMessageRow struct {
	ID           int64
	SessionID    string
	Role         string
	Content      string
	BranchesJSON string
	ActiveBranch int32
	CreatedAt    time.Time
}

// AgentSessionInfo is the per-session summary the runtime's UI
// needs. Derived from agent_messages (no separate table).
type AgentSessionInfo struct {
	ID           string
	MessageCount int32
	LastActive   time.Time
}

// AgentNoteRow mirrors agent_notes.
type AgentNoteRow struct {
	Key       string
	Content   string
	Preview   string
	InContext bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Sentinel errors mirror the runtime's pkg/notes package so the
// Connect handlers can map them to gRPC codes uniformly.
var (
	ErrAgentNoteNotFound   = errors.New("agent note not found")
	ErrAgentNoteInvalidKey = errors.New("invalid note key")
	ErrAgentNoteTooLarge   = errors.New("note content exceeds size cap")
	ErrAgentNoteTooMany    = errors.New("note count would exceed cap")
)

// agentNoteKeyPattern restricts keys to grep-friendly lowercase
// tokens — same shape the runtime's pkg/notes uses today. The
// {0,63} upper bound + the [a-z0-9] anchor implicitly cap at 64
// chars, matching the runtime's enforcement.
var agentNoteKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ── Messages ────────────────────────────────────────────────────

// AgentMessagesList returns up to limit messages (0 = server default
// = 50, matching the runtime's existing LIMIT) ordered oldest-first
// — the hydration pipeline expects chronological order.
func (s *StateStore) AgentMessagesList(
	ctx context.Context, agentID, sessionID string, limit int32,
) ([]AgentMessageRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, role, content, branches_json, active_branch, created_at
		  FROM agent_messages
		 WHERE agent_id = ? AND session_id = ?
		 ORDER BY id ASC
		 LIMIT ?`,
		agentID, sessionID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent messages: %w", err)
	}
	defer rows.Close()

	var out []AgentMessageRow
	for rows.Next() {
		var m AgentMessageRow
		if scanErr := rows.Scan(
			&m.ID, &m.SessionID, &m.Role, &m.Content,
			&m.BranchesJSON, &m.ActiveBranch, &m.CreatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scan agent message: %w", scanErr)
		}
		out = append(out, m)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate agent messages: %w", rowsErr)
	}
	return out, nil
}

// AgentMessagesAppend inserts one message and returns its row id.
// The runtime uses the id later for UpdateBranches.
func (s *StateStore) AgentMessagesAppend(
	ctx context.Context, agentID, sessionID, role, content string,
) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_messages (agent_id, session_id, role, content)
		VALUES (?, ?, ?, ?)`,
		agentID, sessionID, role, content,
	)
	if err != nil {
		return 0, fmt.Errorf("append agent message: %w", err)
	}
	return res.LastInsertId()
}

// AgentMessagesReplace atomically replaces every row for one
// (agent_id, session_id) with the supplied set. The compactor uses
// this after a summarise / slide; previous order is lost.
func (s *StateStore) AgentMessagesReplace(
	ctx context.Context, agentID, sessionID string, msgs []AgentMessageRow,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, delErr := tx.ExecContext(ctx,
		`DELETE FROM agent_messages WHERE agent_id = ? AND session_id = ?`,
		agentID, sessionID,
	); delErr != nil {
		return fmt.Errorf("delete pre-replace: %w", delErr)
	}

	stmt, prepErr := tx.PrepareContext(ctx, `
		INSERT INTO agent_messages
		  (agent_id, session_id, role, content, branches_json, active_branch)
		VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if prepErr != nil {
		return fmt.Errorf("prepare insert: %w", prepErr)
	}
	defer stmt.Close()

	for _, m := range msgs {
		branches := m.BranchesJSON
		if branches == "" {
			branches = "[]"
		}
		if _, execErr := stmt.ExecContext(ctx,
			agentID, sessionID, m.Role, m.Content, branches, m.ActiveBranch,
		); execErr != nil {
			return fmt.Errorf("insert replacement row: %w", execErr)
		}
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return fmt.Errorf("commit replace: %w", commitErr)
	}
	return nil
}

// AgentMessagesUpdateBranches updates one message's content +
// branches_json + active_branch. Used when the model regenerates
// an alternative reply for an existing assistant turn.
func (s *StateStore) AgentMessagesUpdateBranches(
	ctx context.Context, agentID string, id int64,
	content, branchesJSON string, activeBranch int32,
) error {
	if branchesJSON == "" {
		branchesJSON = "[]"
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE agent_messages
		   SET content = ?, branches_json = ?, active_branch = ?
		 WHERE id = ? AND agent_id = ?`,
		content, branchesJSON, activeBranch, id, agentID,
	)
	if err != nil {
		return fmt.Errorf("update branches: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// AgentMessagesLastAssistant returns the most-recent assistant turn
// for a session, or sql.ErrNoRows when there isn't one. Used by the
// runtime's regenerate path.
func (s *StateStore) AgentMessagesLastAssistant(
	ctx context.Context, agentID, sessionID string,
) (AgentMessageRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, role, content, branches_json, active_branch, created_at
		  FROM agent_messages
		 WHERE agent_id = ? AND session_id = ? AND role = 'assistant'
		 ORDER BY id DESC
		 LIMIT 1`,
		agentID, sessionID,
	)
	var m AgentMessageRow
	if err := row.Scan(
		&m.ID, &m.SessionID, &m.Role, &m.Content,
		&m.BranchesJSON, &m.ActiveBranch, &m.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentMessageRow{}, err
		}
		return AgentMessageRow{}, fmt.Errorf("scan last assistant: %w", err)
	}
	return m, nil
}

// AgentMessagesCount returns the total message count for one
// (agent_id, session_id).
func (s *StateStore) AgentMessagesCount(
	ctx context.Context, agentID, sessionID string,
) (int32, error) {
	var n int32
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_messages WHERE agent_id = ? AND session_id = ?`,
		agentID, sessionID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count agent messages: %w", err)
	}
	return n, nil
}

// ── Sessions ────────────────────────────────────────────────────

// AgentSessionsList returns one row per distinct session_id for
// the agent, with message_count + the most recent created_at
// (last_active). Ordered most-recently-active first so the
// session-picker UI doesn't have to sort.
func (s *StateStore) AgentSessionsList(
	ctx context.Context, agentID string,
) ([]AgentSessionInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id,
		       COUNT(*) AS message_count,
		       MAX(created_at) AS last_active
		  FROM agent_messages
		 WHERE agent_id = ?
		 GROUP BY session_id
		 ORDER BY last_active DESC`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent sessions: %w", err)
	}
	defer rows.Close()

	var out []AgentSessionInfo
	for rows.Next() {
		var s AgentSessionInfo
		var lastActive time.Time
		if scanErr := rows.Scan(&s.ID, &s.MessageCount, &lastActive); scanErr != nil {
			return nil, fmt.Errorf("scan agent session: %w", scanErr)
		}
		s.LastActive = lastActive
		out = append(out, s)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate agent sessions: %w", rowsErr)
	}
	return out, nil
}

// AgentSessionsDelete drops every row for one (agent_id, session_id).
func (s *StateStore) AgentSessionsDelete(
	ctx context.Context, agentID, sessionID string,
) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM agent_messages WHERE agent_id = ? AND session_id = ?`,
		agentID, sessionID,
	); err != nil {
		return fmt.Errorf("delete agent session: %w", err)
	}
	return nil
}

// ── Notes ───────────────────────────────────────────────────────

// AgentNotesSave upserts. Mirrors the runtime's pkg/notes Save:
// regex-validates the key, enforces maxBytes (when > 0), and gates
// new keys past maxCount (existing keys can be updated past the
// cap). Returns the saved row + whether it overwrote an existing
// note so the operator UI can render "replaced" vs "created".
func (s *StateStore) AgentNotesSave(
	ctx context.Context, agentID, key, content string, maxBytes, maxCount int,
) (AgentNoteRow, bool, error) {
	key = strings.TrimSpace(key)
	if !agentNoteKeyPattern.MatchString(key) {
		return AgentNoteRow{}, false,
			fmt.Errorf("%w: %q (expected [a-z0-9][a-z0-9_-]{0,63})", ErrAgentNoteInvalidKey, key)
	}
	if maxBytes > 0 && len(content) > maxBytes {
		return AgentNoteRow{}, false,
			fmt.Errorf("%w: %d > %d bytes", ErrAgentNoteTooLarge, len(content), maxBytes)
	}

	var existed bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM agent_notes WHERE agent_id = ? AND key = ?`,
		agentID, key,
	).Scan(new(int)); err == nil {
		existed = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return AgentNoteRow{}, false, fmt.Errorf("probe existing note: %w", err)
	}

	if !existed && maxCount > 0 {
		count, cErr := s.AgentNotesCount(ctx, agentID)
		if cErr != nil {
			return AgentNoteRow{}, false, cErr
		}
		if int(count) >= maxCount {
			return AgentNoteRow{}, false,
				fmt.Errorf("%w: %d notes already stored (cap %d)", ErrAgentNoteTooMany, count, maxCount)
		}
	}

	preview := agentNoteDerivePreview(content)
	now := time.Now().UTC()

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_notes
		  (agent_id, key, content, preview, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id, key) DO UPDATE SET
		  content    = excluded.content,
		  preview    = excluded.preview,
		  updated_at = excluded.updated_at`,
		agentID, key, content, preview, now, now,
	); err != nil {
		return AgentNoteRow{}, false, fmt.Errorf("upsert agent note: %w", err)
	}

	saved, err := s.AgentNotesGet(ctx, agentID, key)
	if err != nil {
		return AgentNoteRow{}, false, err
	}
	return saved, existed, nil
}

// AgentNotesGet returns one note. sql.ErrNoRows when missing.
func (s *StateStore) AgentNotesGet(
	ctx context.Context, agentID, key string,
) (AgentNoteRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT key, content, preview, in_context, created_at, updated_at
		  FROM agent_notes
		 WHERE agent_id = ? AND key = ?`,
		agentID, key,
	)
	var n AgentNoteRow
	var inCtx int
	if err := row.Scan(&n.Key, &n.Content, &n.Preview, &inCtx, &n.CreatedAt, &n.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentNoteRow{}, err
		}
		return AgentNoteRow{}, fmt.Errorf("get agent note: %w", err)
	}
	n.InContext = inCtx == 1
	return n, nil
}

// AgentNotesDelete removes a note. Missing keys are silent
// successes (returns existed=false).
func (s *StateStore) AgentNotesDelete(
	ctx context.Context, agentID, key string,
) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM agent_notes WHERE agent_id = ? AND key = ?`,
		agentID, key,
	)
	if err != nil {
		return false, fmt.Errorf("delete agent note: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	return n > 0, nil
}

// AgentNotesList returns every note for the agent, ordered most-
// recently-updated first.
func (s *StateStore) AgentNotesList(
	ctx context.Context, agentID string, onlyInContext bool,
) ([]AgentNoteRow, error) {
	query := `
		SELECT key, content, preview, in_context, created_at, updated_at
		  FROM agent_notes
		 WHERE agent_id = ?`
	if onlyInContext {
		query += ` AND in_context = 1`
	}
	query += ` ORDER BY updated_at DESC`

	rows, err := s.db.QueryContext(ctx, query, agentID)
	if err != nil {
		return nil, fmt.Errorf("list agent notes: %w", err)
	}
	defer rows.Close()

	var out []AgentNoteRow
	for rows.Next() {
		var n AgentNoteRow
		var inCtx int
		if scanErr := rows.Scan(
			&n.Key, &n.Content, &n.Preview, &inCtx, &n.CreatedAt, &n.UpdatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scan agent note: %w", scanErr)
		}
		n.InContext = inCtx == 1
		out = append(out, n)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate agent notes: %w", rowsErr)
	}
	return out, nil
}

// AgentNotesSetInContext flips the pin flag on one note.
// sql.ErrNoRows when the key doesn't exist for this agent.
func (s *StateStore) AgentNotesSetInContext(
	ctx context.Context, agentID, key string, inContext bool,
) error {
	flag := 0
	if inContext {
		flag = 1
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE agent_notes
		   SET in_context = ?, updated_at = ?
		 WHERE agent_id = ? AND key = ?`,
		flag, time.Now().UTC(), agentID, key,
	)
	if err != nil {
		return fmt.Errorf("set in_context: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// AgentNotesCount returns the agent's total note count.
func (s *StateStore) AgentNotesCount(
	ctx context.Context, agentID string,
) (int32, error) {
	var n int32
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_notes WHERE agent_id = ?`,
		agentID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count agent notes: %w", err)
	}
	return n, nil
}

// agentNoteDerivePreview is a copy of the runtime's preview rule:
// first non-empty line, runs of whitespace collapsed, truncated to
// 80 runes with a trailing ellipsis. Keeping the rule daemon-side
// so the runtime can pass raw content and trust the server's
// rendering.
func agentNoteDerivePreview(content string) string {
	const maxRunes = 80
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		flat := collapseWhitespace(trimmed)
		if utf8.RuneCountInString(flat) > maxRunes {
			runes := []rune(flat)
			return string(runes[:maxRunes-1]) + "…"
		}
		return flat
	}
	return ""
}

func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		b.WriteRune(r)
		inSpace = false
	}
	return b.String()
}
