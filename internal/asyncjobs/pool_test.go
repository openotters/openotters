// White-box test: drives Pool internals (cancels map, dispatch, the
// runOne path) directly. Promoting these to the public API just for
// assertions would widen the surface for no other benefit.
//
//nolint:testpackage // intentional white-box access to private pool internals
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
func (f *fakeAgent) Status() executor.Status                           { return executor.StatusReady }
func (f *fakeAgent) FailureReason() executor.FailureReason             { return executor.FailureNone }
func (f *fakeAgent) StatusTracker() *executor.StatusTracker            { return nil }
func (f *fakeAgent) Probe(_ context.Context) error                     { return nil }
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
// Only used by the Cancel test below, which already synchronises on
// the `<-started` channel; after that, the tail (ctx.Done → sink
// close → MarkCancelled) is bounded by streamFlushInterval × 2 plus
// two trivial SQL writes. 5 s leaves an order-of-magnitude headroom.
func waitFor(t *testing.T, fn func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
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

type execFunc = func(ctx context.Context, bin string, args []string, stdin string) executor.ExecResult

func newPoolFixture(t *testing.T, exec execFunc) (*Pool, *Store, uuid.UUID) {
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

// Runs the dispatch work synchronously via runOne (white-box) instead
// of going through Submit's goroutine dispatch. On slow CI runners
// under -race + -covermode=atomic, the goroutine scheduling tail can
// exceed any reasonable poll budget — this test cared about the work
// runOne does (MarkRunning → Exec → MarkDone), not the trivial
// `go func() { runOne(id) }` wrapper. The async dispatch is exercised
// (and synchronised on a real signal) by the Cancel test below.
func TestPool_RunOne_HappyPath_MarksDone(t *testing.T) {
	pool, store, agentID := newPoolFixture(t,
		func(_ context.Context, _ string, _ []string, _ string) executor.ExecResult {
			return executor.ExecResult{Stdout: "hello\n", ExitCode: 0}
		})

	id, err := store.Insert(context.Background(), Spec{
		AgentID: agentID.String(), Bin: "echo", Args: []string{"hello"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	pool.runOne(id)

	final, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Status != StatusDone {
		t.Fatalf("status = %s, want %s", final.Status, StatusDone)
	}
	if final.Stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", final.Stdout, "hello\n")
	}
	if !final.ExitCode.Valid || final.ExitCode.Int64 != 0 {
		t.Errorf("exit code = %+v, want 0", final.ExitCode)
	}
}

// Submit returns immediately after the row is inserted; the actual
// completion runs on a goroutine. Test only the synchronous half here
// — the goroutine-dispatched work is covered by runOne tests above.
func TestPool_Submit_InsertsPendingRow(t *testing.T) {
	pool, store, agentID := newPoolFixture(t,
		func(_ context.Context, _ string, _ []string, _ string) executor.ExecResult {
			// Block forever so the goroutine cannot transition the
			// row out of pending/running before we observe it.
			select {}
		})
	t.Cleanup(func() {
		// Drain the in-flight goroutine on shutdown so go test doesn't
		// flag a leak. Cancel via Pool.Shutdown, which signals every
		// per-job ctx and waits for the wg.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = pool.Shutdown(ctx)
	})

	id, err := pool.Submit(context.Background(), Spec{
		AgentID: agentID.String(), Bin: "echo", Args: []string{"hello"},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if id == "" {
		t.Fatalf("Submit returned empty id")
	}
	row, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Either status is fine — Submit guarantees the row exists, not
	// that the dispatcher has or hasn't reached MarkRunning yet.
	if row.Status != StatusPending && row.Status != StatusRunning {
		t.Errorf("status = %s, want pending or running", row.Status)
	}
}

func TestPool_Cancel_StopsRunningJob(t *testing.T) {
	started := make(chan struct{})
	pool, store, agentID := newPoolFixture(t,
		func(ctx context.Context, _ string, _ []string, _ string) executor.ExecResult {
			close(started)
			<-ctx.Done()
			return executor.ExecResult{Err: ctx.Err()}
		})

	id, err := pool.Submit(context.Background(), Spec{
		AgentID: agentID.String(), Bin: "sleep", Args: []string{"30"},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	<-started
	if cancelErr := pool.Cancel(id); cancelErr != nil {
		t.Fatalf("Cancel: %v", cancelErr)
	}

	waitForStatus(t, store, id, StatusCancelled)
}

func TestPool_Cancel_UnknownJob_ReturnsErrNotRunning(t *testing.T) {
	pool, _, _ := newPoolFixture(t,
		func(_ context.Context, _ string, _ []string, _ string) executor.ExecResult {
			return executor.ExecResult{}
		})
	if err := pool.Cancel("job_does_not_exist"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Cancel unknown: err = %v, want ErrNotRunning", err)
	}
}

func TestPool_Boot_OrphansRunning(t *testing.T) {
	pool, store, agentID := newPoolFixture(t,
		func(_ context.Context, _ string, _ []string, _ string) executor.ExecResult {
			return executor.ExecResult{}
		})

	// Simulate a prior process: insert a row directly in 'running'.
	id, err := store.Insert(context.Background(), Spec{
		AgentID: agentID.String(), Bin: "ffmpeg", Args: []string{"-i", "in"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if markErr := store.MarkRunning(context.Background(), id, "12345", time.Now().Add(-time.Hour)); markErr != nil {
		t.Fatalf("MarkRunning: %v", markErr)
	}

	if bootErr := pool.Boot(context.Background()); bootErr != nil {
		t.Fatalf("Boot: %v", bootErr)
	}

	final, _ := store.Get(context.Background(), id)
	if final.Status != StatusOrphaned {
		t.Errorf("after Boot: status = %s, want orphaned", final.Status)
	}
}

func TestPool_RunOne_AgentNotRunning_MarksError(t *testing.T) {
	store, _ := newTestStore(t)
	agentID := uuid.New()
	if _, err := store.db.Exec(`INSERT INTO agents (id, name) VALUES (?, ?)`,
		agentID.String(), "fixture"); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	// Empty agent map → Get always returns false.
	pool := NewPool(store, &fakeLookup{agents: map[uuid.UUID]executor.Agent{}},
		zap.NewNop())

	id, err := store.Insert(context.Background(), Spec{
		AgentID: agentID.String(), Bin: "echo", Args: []string{"hi"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	pool.runOne(id)

	final, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Status != StatusError {
		t.Fatalf("status = %s, want %s", final.Status, StatusError)
	}
	if final.Error == "" {
		t.Errorf("error message empty; want a non-empty reason")
	}
}
