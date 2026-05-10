package asyncjobs

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, same as the daemon's primary
)

// newTestStore brings up an isolated SQLite (file in t.TempDir) with
// the async_jobs schema applied + a placeholder agents table so the
// FK reference resolves.
//
// Why not :memory: — modernc.org/sqlite's :memory: is per-connection
// (each handle in database/sql's pool sees its own private DB), so
// the pool's dispatcher goroutine never sees rows the test inserted
// from the test goroutine. A real file in TempDir sidesteps it;
// t.Cleanup() removes the dir.
func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	dbPath := t.TempDir() + "/asyncjobs.db"
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Minimal agents table so the FK resolves in the cascade test.
	if _, err := db.Exec(`CREATE TABLE agents (id TEXT PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("agents schema: %v", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE async_jobs (
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
		)`); err != nil {
		t.Fatalf("async_jobs schema: %v", err)
	}

	// Two agents so we can exercise per-agent filtering / cascade.
	if _, err := db.Exec(`INSERT INTO agents (id, name) VALUES ('a1', 'one'), ('a2', 'two')`); err != nil {
		t.Fatalf("seed agents: %v", err)
	}

	return NewStore(db), db
}

func mustInsert(t *testing.T, s *Store, agent string) string {
	t.Helper()
	id, err := s.Insert(context.Background(), Spec{
		AgentID: agent, Bin: "echo", Args: []string{"hi"}, Stdin: "",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	return id
}

func mustInsertLabeled(t *testing.T, s *Store, agent string, labels map[string]string) string {
	t.Helper()
	id, err := s.Insert(context.Background(), Spec{
		AgentID: agent, Bin: "echo", Args: []string{"hi"}, Labels: labels,
	})
	if err != nil {
		t.Fatalf("Insert labeled: %v", err)
	}
	return id
}

func TestInsertAndGet_RoundTripsAllFields(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)
	id, err := s.Insert(context.Background(), Spec{
		AgentID: "a1", Bin: "sh",
		Args:  []string{"-c", "echo hello"},
		Stdin: "input data",
		Labels: map[string]string{
			"io.openotters.session-id": "cli:chat:abc",
			"io.openotters.origin":     "cli",
			"customer":                 "acme",
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	job, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got, want := job.Bin, "sh"; got != want {
		t.Errorf("Bin = %q, want %q", got, want)
	}
	if got, want := len(job.Args), 2; got != want {
		t.Errorf("len(Args) = %d, want %d", got, want)
	}
	if got, want := job.Args[1], "echo hello"; got != want {
		t.Errorf("Args[1] = %q, want %q", got, want)
	}
	if got, want := job.Stdin, "input data"; got != want {
		t.Errorf("Stdin = %q, want %q", got, want)
	}
	if got, want := job.Status, StatusPending; got != want {
		t.Errorf("Status = %q, want %q", got, want)
	}
	if got, want := job.Labels["io.openotters.session-id"], "cli:chat:abc"; got != want {
		t.Errorf("Labels[session-id] = %q, want %q", got, want)
	}
	if got, want := job.Labels["customer"], "acme"; got != want {
		t.Errorf("Labels[customer] = %q, want %q", got, want)
	}
}

func TestGet_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)
	_, err := s.Get(context.Background(), "job_does_not_exist")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get unknown: err = %v, want ErrNotFound", err)
	}
}

func TestStatusTransitions(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)
	id := mustInsert(t, s, "a1")

	// pending -> running
	if err := s.MarkRunning(context.Background(), id, "1234", time.Now()); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	job, _ := s.Get(context.Background(), id)
	if job.Status != StatusRunning || job.Handle != "1234" {
		t.Errorf("after MarkRunning: status=%s handle=%s", job.Status, job.Handle)
	}

	// running -> done
	if err := s.MarkDone(context.Background(), id, 0, "out", "err", time.Now()); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	job, _ = s.Get(context.Background(), id)
	if job.Status != StatusDone || !job.ExitCode.Valid || job.ExitCode.Int64 != 0 {
		t.Errorf("after MarkDone: status=%s exit=%v", job.Status, job.ExitCode)
	}
	if job.Stdout != "out" || job.Stderr != "err" {
		t.Errorf("output not captured: stdout=%q stderr=%q", job.Stdout, job.Stderr)
	}

	// done -> can't go to running again (status guard)
	err := s.MarkRunning(context.Background(), id, "5678", time.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("re-MarkRunning a done job should return ErrNotFound, got %v", err)
	}
}

func TestMarkOrphaned_BulkUpdatesAllRunningForAgent(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)

	a1Job1 := mustInsert(t, s, "a1")
	a1Job2 := mustInsert(t, s, "a1")
	a2Job := mustInsert(t, s, "a2")

	for _, id := range []string{a1Job1, a1Job2, a2Job} {
		if err := s.MarkRunning(context.Background(), id, id+"-pid", time.Now()); err != nil {
			t.Fatalf("MarkRunning %s: %v", id, err)
		}
	}

	// Orphan only a1's running jobs.
	n, err := s.MarkOrphaned(context.Background(), "a1")
	if err != nil {
		t.Fatalf("MarkOrphaned a1: %v", err)
	}
	if n != 2 {
		t.Errorf("MarkOrphaned a1 affected %d, want 2", n)
	}

	// a2's job stays running.
	a2, _ := s.Get(context.Background(), a2Job)
	if a2.Status != StatusRunning {
		t.Errorf("a2 job should still be running, got %s", a2.Status)
	}

	// Calling with empty agentID orphans the rest.
	n, _ = s.MarkOrphaned(context.Background(), "")
	if n != 1 {
		t.Errorf("MarkOrphaned all affected %d, want 1 (just a2)", n)
	}
}

func TestListByAgent_FiltersByStatus(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)

	pending := mustInsert(t, s, "a1")
	running := mustInsert(t, s, "a1")
	_ = pending
	_ = mustInsert(t, s, "a2")

	if err := s.MarkRunning(context.Background(), running, "rpid", time.Now()); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}

	all, err := s.ListByAgent(context.Background(), "a1", ListFilter{})
	if err != nil {
		t.Fatalf("ListByAgent all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("ListByAgent(a1, all) = %d rows, want 2", len(all))
	}

	pendingOnly, err := s.ListByAgent(context.Background(), "a1",
		ListFilter{Statuses: []Status{StatusPending}})
	if err != nil {
		t.Fatalf("ListByAgent pending: %v", err)
	}
	if len(pendingOnly) != 1 {
		t.Errorf("ListByAgent(a1, pending) = %d rows, want 1", len(pendingOnly))
	}
}

func TestListByAgent_FiltersByLabels(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)

	// Three jobs on a1 with overlapping labels; one on a2.
	mustInsertLabeled(t, s, "a1", map[string]string{
		"io.openotters.session-id": "S1",
		"io.openotters.origin":     "cli",
	})
	mustInsertLabeled(t, s, "a1", map[string]string{
		"io.openotters.session-id": "S1",
		"io.openotters.origin":     "chat",
	})
	mustInsertLabeled(t, s, "a1", map[string]string{
		"io.openotters.session-id": "S2",
	})
	mustInsertLabeled(t, s, "a2", map[string]string{
		"io.openotters.session-id": "S1",
	})

	// Single-key match: every a1 job tagged with S1.
	got, err := s.ListByAgent(context.Background(), "a1", ListFilter{
		Labels: map[string]string{"io.openotters.session-id": "S1"},
	})
	if err != nil {
		t.Fatalf("ListByAgent label filter: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("S1 jobs on a1: got %d, want 2", len(got))
	}

	// Two-key AND: only the cli-origin one matches.
	got, err = s.ListByAgent(context.Background(), "a1", ListFilter{
		Labels: map[string]string{
			"io.openotters.session-id": "S1",
			"io.openotters.origin":     "cli",
		},
	})
	if err != nil {
		t.Fatalf("ListByAgent label AND: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("S1+cli on a1: got %d, want 1", len(got))
	}

	// Missing key never matches — even when value matches some other key.
	got, err = s.ListByAgent(context.Background(), "a1", ListFilter{
		Labels: map[string]string{"never-set": "S1"},
	})
	if err != nil {
		t.Fatalf("ListByAgent missing label: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing-label filter: got %d, want 0", len(got))
	}
}

func TestListPending_OldestFirst_RespectsLimit(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)
	id1 := mustInsert(t, s, "a1")
	time.Sleep(1100 * time.Millisecond) // distinct created_at seconds
	id2 := mustInsert(t, s, "a2")
	_ = id2

	got, err := s.ListPending(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(got) != 2 || got[0].ID != id1 {
		t.Errorf("ListPending order/count off: got %d rows, first ID = %q", len(got), got[0].ID)
	}

	limited, _ := s.ListPending(context.Background(), 1)
	if len(limited) != 1 || limited[0].ID != id1 {
		t.Errorf("ListPending with limit=1 should return oldest row; got %d rows", len(limited))
	}
}

func TestDeleteByAgent_RemovesAllRows(t *testing.T) {
	t.Parallel()
	s, _ := newTestStore(t)
	mustInsert(t, s, "a1")
	mustInsert(t, s, "a1")
	mustInsert(t, s, "a2")

	n, err := s.DeleteByAgent(context.Background(), "a1")
	if err != nil {
		t.Fatalf("DeleteByAgent: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteByAgent a1 affected %d, want 2", n)
	}

	left, _ := s.ListByAgent(context.Background(), "a1", ListFilter{})
	if len(left) != 0 {
		t.Errorf("a1 should have no rows left, got %d", len(left))
	}
	left2, _ := s.ListByAgent(context.Background(), "a2", ListFilter{})
	if len(left2) != 1 {
		t.Errorf("a2's row should be untouched, got %d", len(left2))
	}
}

func TestForeignKeyCascade_OnAgentDelete(t *testing.T) {
	t.Parallel()
	s, db := newTestStore(t)
	id := mustInsert(t, s, "a1")

	// Drop the agent — FK cascade should take the job with it.
	if _, err := db.Exec(`DELETE FROM agents WHERE id = 'a1'`); err != nil {
		t.Fatalf("delete agent: %v", err)
	}

	_, err := s.Get(context.Background(), id)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FK cascade should have removed the job; Get err = %v, want ErrNotFound", err)
	}
}
