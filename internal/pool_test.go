// White-box test: writeFailureLog, runLoop, and the supervisor's
// retryCancel plumbing are unexported; promoting them widens the
// public API for assertions only.
//
//nolint:testpackage // intentional white-box access to private helpers
package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	agentpkg "github.com/openotters/agentfile/agent"
)

func TestWriteFailureLog_AppendsOneLineFromJoinedError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	id := uuid.New()

	p := &Pool{logger: zap.NewNop(), logDir: dir}

	// Mirror the real failure shape: errors.Join(sentinel, wrapped cause).
	// The helper must collapse the embedded newline so the file shows a
	// single line per failure — that is the contract `otters logs` relies on.
	sentinel := errors.New("model resolve error")
	cause := fmt.Errorf("resolving model: provider %q not configured in ~/.otters/providers.yaml", "anthropic")

	p.writeFailureLog(id, "model_error", errors.Join(sentinel, cause))

	data, err := os.ReadFile(filepath.Join(dir, id.String()+".log"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	got := string(data)

	if strings.Count(got, "\n") != 1 {
		t.Fatalf("expected exactly one newline (one log entry), got %q", got)
	}

	for _, want := range []string{
		"model_error:",
		"model resolve error",
		`provider "anthropic" not configured in ~/.otters/providers.yaml`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log missing %q; got %q", want, got)
		}
	}
}

func TestWriteFailureLog_NoopWhenLogDirUnset(t *testing.T) {
	t.Parallel()

	// logDir == "" must short-circuit before any file IO. Daemon callers
	// that didn't wire WithLogDir (e.g. some tests) shouldn't accidentally
	// create files in the cwd.
	p := &Pool{logger: zap.NewNop()}

	p.writeFailureLog(uuid.New(), "init_error", errors.New("boom"))
}

// fakeAgent satisfies agentpkg.Agent for pool supervisor tests. Each
// Run/Start call records its invocation and returns the next scripted
// outcome. Status follows the scripted outcome — error outcomes leave
// status in an *_error state so the supervisor's auto-restart kicks in;
// nil outcomes leave it Stopped.
type fakeAgent struct {
	id uuid.UUID

	mu       sync.Mutex
	tracker  *agentpkg.StatusTracker
	outcomes []fakeOutcome
	calls    atomic.Int64
}

type fakeOutcome struct {
	err    error           // returned by Run/Start
	status agentpkg.Status // status set before returning
	hold   chan struct{}   // if non-nil, Run blocks on it until closed
}

func newFakeAgent(outcomes ...fakeOutcome) *fakeAgent {
	return &fakeAgent{
		id:       uuid.New(),
		tracker:  agentpkg.NewStatusTracker(),
		outcomes: outcomes,
	}
}

func (f *fakeAgent) UUID() uuid.UUID                                   { return f.id }
func (f *fakeAgent) Runtime() *agentpkg.AgentRuntime                   { return nil }
func (f *fakeAgent) Prepare(_ context.Context) error                   { return nil }
func (f *fakeAgent) Stop(_ context.Context) error                      { return nil }
func (f *fakeAgent) Remove(_ context.Context) error                    { return nil }
func (f *fakeAgent) Status() agentpkg.Status                           { return f.tracker.Get() }
func (f *fakeAgent) SubscribeStatus() (<-chan agentpkg.Status, func()) { return f.tracker.Subscribe() }

func (f *fakeAgent) nextOutcome() fakeOutcome {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.outcomes) == 0 {
		return fakeOutcome{status: agentpkg.StatusStopped}
	}

	out := f.outcomes[0]
	f.outcomes = f.outcomes[1:]

	return out
}

func (f *fakeAgent) attempt(ctx context.Context) error {
	f.calls.Add(1)
	out := f.nextOutcome()

	if out.hold != nil {
		select {
		case <-out.hold:
		case <-ctx.Done():
			f.tracker.Set(agentpkg.StatusStopped)

			return ctx.Err()
		}
	}

	f.tracker.Set(out.status)

	return out.err
}

func (f *fakeAgent) Run(ctx context.Context) error   { return f.attempt(ctx) }
func (f *fakeAgent) Start(ctx context.Context) error { return f.attempt(ctx) }

// --- backoffDelay --------------------------------------------------------

func TestBackoffDelay_GrowsThenCaps(t *testing.T) {
	t.Parallel()

	p := &Pool{backoffBase: time.Second, backoffCap: 10 * time.Second}

	cases := map[int]time.Duration{
		0:  1 * time.Second,
		1:  2 * time.Second,
		2:  4 * time.Second,
		3:  8 * time.Second,
		4:  10 * time.Second, // 16 → capped at 10
		10: 10 * time.Second,
		33: 10 * time.Second, // overflow guard
		-1: 1 * time.Second,  // attempt 0 floor
	}

	for attempt, want := range cases {
		if got := p.backoffDelay(attempt); got != want {
			t.Errorf("backoffDelay(%d) = %v, want %v", attempt, got, want)
		}
	}
}

func TestIsErrorStatus(t *testing.T) {
	t.Parallel()

	cases := map[agentpkg.Status]bool{
		agentpkg.StatusInitError:  true,
		agentpkg.StatusPullError:  true,
		agentpkg.StatusModelError: true,
		agentpkg.StatusRunning:    false,
		agentpkg.StatusStopped:    false,
		agentpkg.StatusCreated:    false,
		agentpkg.StatusRemoving:   false,
		agentpkg.StatusRemoved:    false,
	}

	for s, want := range cases {
		if got := isErrorStatus(s); got != want {
			t.Errorf("isErrorStatus(%v) = %v, want %v", s, got, want)
		}
	}
}

// --- runLoop -------------------------------------------------------------

func newTestPool(t *testing.T) *Pool {
	t.Helper()

	return &Pool{
		logger:      zap.NewNop(),
		sem:         make(chan struct{}, 4),
		agents:      make(map[uuid.UUID]*pooledAgent),
		backoffBase: time.Millisecond,
		backoffCap:  5 * time.Millisecond,
	}
}

func TestRunLoop_RetriesUntilSuccess(t *testing.T) {
	t.Parallel()

	a := newFakeAgent(
		fakeOutcome{err: errors.New("model resolve error"), status: agentpkg.StatusModelError},
		fakeOutcome{err: errors.New("model resolve error"), status: agentpkg.StatusModelError},
		fakeOutcome{err: nil, status: agentpkg.StatusStopped}, // success
	)

	p := newTestPool(t)
	retryCtx, retryCancel := context.WithCancel(context.Background())
	defer retryCancel()

	done := make(chan struct{})
	go func() {
		p.runLoop(uuid.New(), "ref", a, retryCtx, true)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runLoop did not exit after a successful attempt")
	}

	if got := a.calls.Load(); got != 3 {
		t.Fatalf("attempt count = %d, want 3 (two failures + one success)", got)
	}
}

func TestRunLoop_StopsOnNonErrorStatus(t *testing.T) {
	t.Parallel()

	// First attempt returns an error but with Stopped status (e.g.
	// runtime crashed mid-run, signal exit). Auto-restart must NOT
	// kick in — that's a different recovery path.
	a := newFakeAgent(fakeOutcome{
		err:    errors.New("exit status 137"),
		status: agentpkg.StatusStopped,
	})

	p := newTestPool(t)
	retryCtx, retryCancel := context.WithCancel(context.Background())
	defer retryCancel()

	done := make(chan struct{})
	go func() {
		p.runLoop(uuid.New(), "ref", a, retryCtx, true)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runLoop did not exit on Stopped+err")
	}

	if got := a.calls.Load(); got != 1 {
		t.Fatalf("attempt count = %d, want 1 (no retry on Stopped)", got)
	}
}

func TestRunLoop_RetryCancelExitsCleanly(t *testing.T) {
	t.Parallel()

	// Infinite-failure script. retryCtx cancellation must abort the
	// backoff sleep and exit the supervisor — this is what Pool.Stop /
	// Pool.Remove rely on.
	outcomes := make([]fakeOutcome, 100)
	for i := range outcomes {
		outcomes[i] = fakeOutcome{err: errors.New("boom"), status: agentpkg.StatusInitError}
	}

	a := newFakeAgent(outcomes...)

	p := newTestPool(t)
	p.backoffBase = 50 * time.Millisecond // long enough that we cancel mid-sleep

	retryCtx, retryCancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		p.runLoop(uuid.New(), "ref", a, retryCtx, true)
		close(done)
	}()

	// Let the supervisor fail at least once and enter the backoff sleep.
	time.Sleep(20 * time.Millisecond)
	retryCancel()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runLoop did not exit after retryCtx cancel")
	}

	calls := a.calls.Load()
	if calls < 1 {
		t.Fatalf("attempt count = %d, want at least 1", calls)
	}

	if calls > 5 {
		t.Errorf("attempt count = %d, suspiciously high — backoff may not be honoured", calls)
	}
}

// --- Pool.Stop / Remove cancel pending backoff --------------------------

func TestPoolStop_CancelsPendingRetry(t *testing.T) {
	t.Parallel()

	outcomes := make([]fakeOutcome, 100)
	for i := range outcomes {
		outcomes[i] = fakeOutcome{err: errors.New("boom"), status: agentpkg.StatusInitError}
	}

	a := newFakeAgent(outcomes...)
	id := a.UUID()

	p := newTestPool(t)
	p.backoffBase = 100 * time.Millisecond
	p.backoffCap = 100 * time.Millisecond

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	if err := p.Init(rootCtx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	done := make(chan struct{})
	retryCtx, retryCancel := context.WithCancel(p.rootCtx)

	p.mu.Lock()
	p.agents[id] = &pooledAgent{agent: a, done: done, retryCancel: retryCancel}
	p.mu.Unlock()

	go func() {
		defer close(done)
		defer retryCancel()
		p.runLoop(id, "ref", a, retryCtx, true)
	}()

	// Let one failure happen so the supervisor is in backoff.
	time.Sleep(30 * time.Millisecond)

	if err := p.Stop(context.Background(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("supervisor did not exit after Pool.Stop")
	}
}

func TestWriteFailureLog_AppendsAcrossCalls(t *testing.T) {
	t.Parallel()

	// Re-running a failing agent (Start after Stop) should append, not
	// truncate — operators expect a history of attempts.
	dir := t.TempDir()
	id := uuid.New()

	p := &Pool{logger: zap.NewNop(), logDir: dir}

	p.writeFailureLog(id, "model_error", errors.New("first"))
	p.writeFailureLog(id, "init_error", errors.New("second"))

	data, err := os.ReadFile(filepath.Join(dir, id.String()+".log"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	got := string(data)
	if strings.Count(got, "\n") != 2 {
		t.Fatalf("expected two log entries, got %q", got)
	}

	if !strings.Contains(got, "model_error: first") || !strings.Contains(got, "init_error: second") {
		t.Fatalf("missing one of the entries; got %q", got)
	}
}
