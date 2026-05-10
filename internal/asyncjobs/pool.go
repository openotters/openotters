package asyncjobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/openotters/agentfile/executor"
)

// AgentLookup is the slice of the daemon's agent pool that asyncjobs
// needs: given an agent UUID, return its running Agent or false if
// it's not running. Decoupled into an interface so the package can
// be unit-tested without standing up the full daemon.
type AgentLookup interface {
	Get(id uuid.UUID) (executor.Agent, bool)
}

// Pool dispatches async jobs and owns the per-job goroutine
// lifecycle. Bounded by a semaphore to keep host load predictable.
//
// One Pool per daemon. Submit ⇒ Insert + dispatch; the dispatcher
// goroutine runs the BIN against the agent's spawn env and writes
// the terminal status. Cancellation flips a per-job ctx; Boot replays
// pending rows on daemon startup; Shutdown drains in-flight goroutines
// gracefully.
//
// Job results are NOT pushed back to the agent's session — the daemon
// just persists status / stdout / stderr / exit_code to the row. Any
// observer (agent runtime, operator CLI, UI) reads via GetAsyncJob /
// ListAsyncJobs RPCs. Watch semantics are the consumer's concern.
type Pool struct {
	store  *Store
	agents AgentLookup
	logger *zap.Logger

	sem     chan struct{} // semaphore — buffered, capacity = max concurrent
	cancels sync.Map      // jobID(string) -> context.CancelFunc
	wg      sync.WaitGroup
}

// PoolOption follows the Functional Options pattern used elsewhere
// in the codebase (NewDaemon, etc.).
type PoolOption func(*Pool)

// WithMaxConcurrent caps the number of in-flight jobs. Default 10.
// A non-positive value falls back to the default.
func WithMaxConcurrent(n int) PoolOption {
	return func(p *Pool) {
		if n > 0 {
			p.sem = make(chan struct{}, n)
		}
	}
}

// NewPool constructs the dispatcher. logger is required (use
// zap.NewNop() in tests).
func NewPool(store *Store, agents AgentLookup, logger *zap.Logger, opts ...PoolOption) *Pool {
	if logger == nil {
		logger = zap.NewNop()
	}
	p := &Pool{
		store:  store,
		agents: agents,
		logger: logger.Named("asyncjobs"),
		sem:    make(chan struct{}, 10),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Submit registers a new job and dispatches it for execution. Returns
// the job ID immediately; the actual BIN run happens on a goroutine
// bounded by the pool's semaphore.
func (p *Pool) Submit(ctx context.Context, spec Spec) (string, error) {
	id, err := p.store.Insert(ctx, spec)
	if err != nil {
		return "", err
	}
	p.dispatch(id)
	return id, nil
}

// Cancel signals the per-job ctx to stop the underlying execution.
// Returns ErrNotRunning when the job isn't currently in flight (it
// may already be terminal, or never have been dispatched yet).
func (p *Pool) Cancel(jobID string) error {
	v, ok := p.cancels.Load(jobID)
	if !ok {
		return ErrNotRunning
	}
	v.(context.CancelFunc)()
	return nil
}

// Boot runs once at daemon startup, before the gRPC listener accepts
// traffic. Two responsibilities:
//
//  1. Any row in `running` from a prior process is dead by definition
//     (the goroutine that owned it is gone). Mark every such row as
//     `orphaned` so observers polling the row see a clean terminal
//     state instead of a perpetually-`running` ghost.
//  2. Any row in `pending` was created right before shutdown and
//     never picked up. Re-dispatch them.
//
// Note: this does not attempt to KILL stale OS processes from a
// previous incarnation — for v1 the system backend accepts that
// ungraceful shutdowns may leave zombies (operator can `pkill -f`).
// The docker backend handles its own label-based ghost sweep at
// agent-runtime startup time.
func (p *Pool) Boot(ctx context.Context) error {
	runners, err := p.store.ListRunning(ctx)
	if err != nil {
		return fmt.Errorf("Boot: list running: %w", err)
	}

	if _, err := p.store.MarkOrphaned(ctx, ""); err != nil {
		return fmt.Errorf("Boot: mark orphaned: %w", err)
	}

	pending, err := p.store.ListPending(ctx, 1000)
	if err != nil {
		return fmt.Errorf("Boot: list pending: %w", err)
	}

	for _, j := range pending {
		p.dispatch(j.ID)
	}

	if len(runners) > 0 || len(pending) > 0 {
		p.logger.Info("boot replay",
			zap.Int("orphaned", len(runners)),
			zap.Int("redispatched", len(pending)),
		)
	}
	return nil
}

// Shutdown cancels every in-flight job and waits for the dispatcher
// goroutines to return, bounded by ctx. Rows still in `running`
// after cancel get marked `cancelled` by their respective goroutines
// before they exit (the agent.Exec ctx is what we cancel).
func (p *Pool) Shutdown(ctx context.Context) error {
	p.cancels.Range(func(_, v any) bool {
		v.(context.CancelFunc)()
		return true
	})

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ─── private ────────────────────────────────────────────────────────

func (p *Pool) dispatch(jobID string) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		// Acquire the semaphore. Blocks if max concurrency reached.
		p.sem <- struct{}{}
		defer func() { <-p.sem }()
		p.runOne(jobID)
	}()
}

// runOne loads the job, dispatches to the agent's Exec, and writes
// the terminal status (done / error / cancelled). All errors are
// recorded in the row so observers polling GetAsyncJob see a
// consistent terminal state.
func (p *Pool) runOne(jobID string) {
	bg := context.Background()

	job, err := p.store.Get(bg, jobID)
	if err != nil {
		p.logger.Warn("job vanished before dispatch",
			zap.String("id", jobID), zap.Error(err))
		return
	}

	agentID, parseErr := uuid.Parse(job.AgentID)
	if parseErr != nil {
		_ = p.store.MarkError(bg, jobID,
			"agent_id is not a valid UUID: "+job.AgentID, time.Now())
		return
	}

	agent, ok := p.agents.Get(agentID)
	if !ok {
		_ = p.store.MarkError(bg, jobID,
			"agent not running at dispatch", time.Now())
		return
	}

	jobCtx, cancel := context.WithCancel(bg)
	p.cancels.Store(jobID, context.CancelFunc(cancel))
	defer func() {
		p.cancels.Delete(jobID)
		cancel()
	}()

	if err := p.store.MarkRunning(bg, jobID, "", time.Now()); err != nil {
		p.logger.Warn("MarkRunning failed",
			zap.String("id", jobID), zap.Error(err))
		return
	}

	result := agent.Exec(jobCtx, job.Bin, job.Args, job.Stdin)

	if result.Handle != "" {
		_ = p.store.SetHandle(bg, jobID, result.Handle)
	}

	finished := time.Now()
	switch {
	case result.Err != nil && errors.Is(result.Err, context.Canceled):
		_ = p.store.MarkCancelled(bg, jobID, result.Stdout, result.Stderr, finished)
	case result.Err != nil:
		_ = p.store.MarkError(bg, jobID, result.Err.Error(), finished)
	default:
		_ = p.store.MarkDone(bg, jobID, result.ExitCode, result.Stdout, result.Stderr, finished)
	}
}

// ErrNotRunning is returned by Cancel when the job isn't currently
// in flight (already terminal, or never dispatched).
var ErrNotRunning = errors.New("async job not currently running")
