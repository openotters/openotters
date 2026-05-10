package asyncjobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/openotters/agentfile/executor"
)

// fakeAgent satisfies executor.Agent for pool tests. Only Exec is
// exercised — the other lifecycle methods are no-ops since the pool
// doesn't touch them.
type fakeAgent struct {
	id       uuid.UUID
	execFunc func(ctx context.Context, bin string, args []string, stdin string) executor.ExecResult
}

func (f *fakeAgent) UUID() uuid.UUID                                   { return f.id }
func (f *fakeAgent) Runtime() *executor.Runtime                        { return nil }
func (f *fakeAgent) Prepare(_ context.Context) error                   { return nil }
func (f *fakeAgent) Run(_ context.Context) error                       { return nil }
func (f *fakeAgent) Start(_ context.Context) error                     { return nil }
func (f *fakeAgent) Stop(_ context.Context) error                      { return nil }
func (f *fakeAgent) Remove(_ context.Context) error                    { return nil }
func (f *fakeAgent) Status() executor.Status                           { return executor.StatusRunning }
func (f *fakeAgent) SubscribeStatus() (<-chan executor.Status, func()) { return nil, func() {} }
func (f *fakeAgent) Exec(ctx context.Context, bin string, args []string, stdin string) executor.ExecResult {
	return f.execFunc(ctx, bin, args, stdin)
}

// fakeLookup is a tiny AgentLookup keyed by UUID.
type fakeLookup struct{ agents map[uuid.UUID]executor.Agent }

func (l *fakeLookup) Get(id uuid.UUID) (executor.Agent, bool) {
	a, ok := l.agents[id]
	return a, ok
}

// waitFor polls fn() until it returns true or the deadline hits.
// Useful because the pool dispatches on a goroutine.
func waitFor(t *testing.T, fn func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waitFor timed out: %s", msg)
}

// waitForStatus is the canonical end-state signal for a job. The
// daemon doesn't push completion anywhere — observers (and tests)
// poll the row.
func waitForStatus(t *testing.T, store *Store, id string, want Status) *Job {
	t.Helper()
	var final *Job
	waitFor(t, func() bool {
		j, _ := store.Get(context.Background(), id)
		if j != nil && j.Status == want {
			final = j
			return true
		}
		return false
	}, "job to reach status="+string(want))
	return final
}

func newPoolFixture(t *testing.T, exec func(ctx context.Context, bin string, args []string, stdin string) executor.ExecResult) (*Pool, *Store, uuid.UUID) {
	t.Helper()
	store, _ := newTestStore(t)
	agentID := uuid.New()

	// Insert the agent row so the FK resolves.
	if _, err := store.db.Exec(`INSERT INTO agents (id, name) VALUES (?, ?)`,
		agentID.String(), "fixture"); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	pool := NewPool(store,
		&fakeLookup{agents: map[uuid.UUID]executor.Agent{
			agentID: &fakeAgent{id: agentID, execFunc: exec},
		}},
		zap.NewNop(),
	)
	return pool, store, agentID
}

func TestPool_Submit_RunsToCompletion(t *testing.T) {
	t.Parallel()
	pool, store, agentID := newPoolFixture(t,
		func(_ context.Context, _ string, _ []string, _ string) executor.ExecResult {
			return executor.ExecResult{Stdout: "hello\n", ExitCode: 0}
		})

	id, err := pool.Submit(context.Background(), Spec{
		AgentID: agentID.String(),Bin: "echo", Args: []string{"hello"},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	final := waitForStatus(t, store, id, StatusDone)
	if final.Stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", final.Stdout, "hello\n")
	}
	if !final.ExitCode.Valid || final.ExitCode.Int64 != 0 {
		t.Errorf("exit code = %+v, want 0", final.ExitCode)
	}
}

func TestPool_Cancel_StopsRunningJob(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	pool, store, agentID := newPoolFixture(t,
		func(ctx context.Context, _ string, _ []string, _ string) executor.ExecResult {
			close(started)
			<-ctx.Done()
			return executor.ExecResult{Err: ctx.Err()}
		})

	id, err := pool.Submit(context.Background(), Spec{
		AgentID: agentID.String(),Bin: "sleep", Args: []string{"30"},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	<-started
	if err := pool.Cancel(id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	waitForStatus(t, store, id, StatusCancelled)
}

func TestPool_Cancel_UnknownJob_ReturnsErrNotRunning(t *testing.T) {
	t.Parallel()
	pool, _, _ := newPoolFixture(t,
		func(_ context.Context, _ string, _ []string, _ string) executor.ExecResult {
			return executor.ExecResult{}
		})
	if err := pool.Cancel("job_does_not_exist"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Cancel unknown: err = %v, want ErrNotRunning", err)
	}
}

func TestPool_Boot_OrphansRunning(t *testing.T) {
	t.Parallel()
	pool, store, agentID := newPoolFixture(t,
		func(_ context.Context, _ string, _ []string, _ string) executor.ExecResult {
			return executor.ExecResult{}
		})

	// Simulate a prior process: insert a row directly in 'running'.
	id, err := store.Insert(context.Background(), Spec{
		AgentID: agentID.String(),Bin: "ffmpeg", Args: []string{"-i", "in"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := store.MarkRunning(context.Background(), id, "12345", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}

	if err := pool.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	final, _ := store.Get(context.Background(), id)
	if final.Status != StatusOrphaned {
		t.Errorf("after Boot: status = %s, want orphaned", final.Status)
	}
}

func TestPool_RunOne_AgentNotRunning_MarksError(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	agentID := uuid.New()
	if _, err := store.db.Exec(`INSERT INTO agents (id, name) VALUES (?, ?)`,
		agentID.String(), "fixture"); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	// Empty agent map → Get always returns false.
	pool := NewPool(store, &fakeLookup{agents: map[uuid.UUID]executor.Agent{}},
		zap.NewNop())

	id, err := pool.Submit(context.Background(), Spec{
		AgentID: agentID.String(),Bin: "echo", Args: []string{"hi"},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	final := waitForStatus(t, store, id, StatusError)
	if final.Error == "" {
		t.Errorf("error message empty; want a non-empty reason")
	}
}
