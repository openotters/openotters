package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	agentpkg "github.com/openotters/agentfile/executor"
	"github.com/openotters/agentfile/executor/system"
	"github.com/openotters/agentfile/spec"
)

const (
	defaultMaxConcurrent = 4
	defaultBackoffBase   = time.Second
	defaultBackoffCap    = 30 * time.Second
)

// Pool manages a set of agents bound to a Provider. Adds are fire-and-forget:
// each Add spawns a supervisor goroutine that creates the agent via Provider
// and invokes Run. A semaphore bounds the number of concurrently running
// agents; extra adds block until a slot frees.
//
// The supervisor implements auto-restart with exponential backoff: when a
// Run/Start attempt returns and the agent is in an error status
// (init_error / pull_error / model_error), the supervisor sleeps for
// backoffBase * 2^attempt (capped at backoffCap) and retries. Recovery
// is automatic — fix providers.yaml or restore the registry, and the
// next backoff window picks up the change. Manual Pool.Stop / Pool.Remove
// cancel any pending backoff so a stop is honoured immediately.
type Pool struct {
	provider    agentpkg.Provider
	logger      *zap.Logger
	logDir      string
	sem         chan struct{}
	backoffBase time.Duration
	backoffCap  time.Duration

	mu     sync.Mutex
	agents map[uuid.UUID]*pooledAgent

	rootCtx    context.Context
	rootCancel context.CancelFunc
	started    bool
}

type pooledAgent struct {
	agent       agentpkg.Agent
	done        chan struct{}
	retryCancel context.CancelFunc
}

// PoolOption configures a Pool at construction time.
type PoolOption func(*Pool)

// WithMaxConcurrent caps the number of agents that may be running at once.
// Additional Add calls block in their spawned goroutine until a slot frees.
func WithMaxConcurrent(n int) PoolOption {
	return func(p *Pool) {
		if n < 1 {
			n = 1
		}

		p.sem = make(chan struct{}, n)
	}
}

// WithLogger attaches a logger so Create/Run errors are visible instead of
// silently swallowed. Defaults to zap.NewNop when unset.
func WithLogger(l *zap.Logger) PoolOption {
	return func(p *Pool) { p.logger = l }
}

// WithLogDir directs the pool to append a one-line failure summary to
// <dir>/<agent-id>.log whenever Run/Start returns with a non-nil error.
// Surfaces init / pull / model_error causes via `otters logs` even
// though the runtime subprocess never produced output of its own.
// Unset disables the per-agent file write; the zap logger still gets
// the entry.
func WithLogDir(dir string) PoolOption {
	return func(p *Pool) { p.logDir = dir }
}

// WithBackoffBase overrides the auto-restart base delay. The schedule
// is base, base*2, base*4, … capped by WithBackoffCap. Production
// keeps the 1s default; tests pass a sub-millisecond value to keep
// retry-loop tests fast.
func WithBackoffBase(d time.Duration) PoolOption {
	return func(p *Pool) {
		if d > 0 {
			p.backoffBase = d
		}
	}
}

// WithBackoffCap caps the maximum backoff delay between auto-restart
// attempts. Defaults to 30s.
func WithBackoffCap(d time.Duration) PoolOption {
	return func(p *Pool) {
		if d > 0 {
			p.backoffCap = d
		}
	}
}

// NewPool returns a Pool that creates agents via provider.
func NewPool(provider agentpkg.Provider, opts ...PoolOption) *Pool {
	p := &Pool{
		provider:    provider,
		logger:      zap.NewNop(),
		agents:      make(map[uuid.UUID]*pooledAgent),
		sem:         make(chan struct{}, defaultMaxConcurrent),
		backoffBase: defaultBackoffBase,
		backoffCap:  defaultBackoffCap,
	}

	for _, o := range opts {
		o(p)
	}

	return p
}

// Init binds the pool lifecycle to ctx synchronously. Must be called
// before any Add / Start so runNew / runExisting see a real rootCtx
// (instead of falling back to Background and leaking uncancellable
// agent goroutines on shutdown). Pairs with Wait, which blocks until
// ctx cancels and then drains.
func (p *Pool) Init(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started {
		return fmt.Errorf("pool already started")
	}

	p.started = true
	p.rootCtx, p.rootCancel = context.WithCancel(ctx)

	return nil
}

// Wait blocks until the pool's root context is cancelled, then
// signals every running agent to stop and waits (up to 30s) for their
// goroutines to exit. Must be called after Init.
func (p *Pool) Wait() error {
	p.mu.Lock()
	rootCtx := p.rootCtx
	rootCancel := p.rootCancel
	p.mu.Unlock()

	if rootCtx == nil {
		return fmt.Errorf("pool not initialized; call Init first")
	}

	<-rootCtx.Done()

	// rootCancel is the cancel paired with WithCancel above. The ctx is
	// already done at this point, but calling cancel keeps go vet happy
	// and ensures any future caller that switches to WithTimeout doesn't
	// leak its timer.
	defer rootCancel()

	return p.stopAll(rootCtx)
}

// Add creates and runs an agent in the background. The returned agent is
// observable via Get once Create has succeeded. agentOpts is an optional
// slice of system-provider-specific AgentOption values — today used for
// bind-mounts; extensions (stdin, resource limits, …) slot in here
// without touching the signature.
func (p *Pool) Add(
	id uuid.UUID, ref spec.Reference,
	agentOpts []system.AgentOption, overrides ...spec.Override,
) {
	go p.runNew(id, ref, agentOpts, overrides)
}

// Start re-runs an existing agent that was previously stopped or
// drained out of its auto-restart backoff. Cancels any prior
// supervisor for the same id, waits briefly for it to exit, then
// spawns a fresh supervisor (whose attempt counter starts at zero —
// manual restart resets the backoff schedule). Non-blocking once the
// prior supervisor has drained.
func (p *Pool) Start(id uuid.UUID) error {
	p.mu.Lock()
	pa, ok := p.agents[id]
	if !ok {
		p.mu.Unlock()

		return fmt.Errorf("agent %s not in pool", id)
	}

	priorRetryCancel := pa.retryCancel
	priorDone := pa.done
	p.mu.Unlock()

	if priorRetryCancel != nil {
		priorRetryCancel()
	}

	if priorDone != nil {
		select {
		case <-priorDone:
		case <-time.After(5 * time.Second):
			return fmt.Errorf("previous supervisor for %s did not exit", id)
		}
	}

	done := make(chan struct{})

	p.mu.Lock()
	pa.done = done
	pa.retryCancel = nil // runExisting will install its own
	p.mu.Unlock()

	go p.runExisting(id, pa.agent, done)

	return nil
}

// Get returns the agent for id if Create has completed, or nil, false.
func (p *Pool) Get(id uuid.UUID) (agentpkg.Agent, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	pa, ok := p.agents[id]
	if !ok {
		return nil, false
	}

	return pa.agent, true
}

// Stop cancels any pending auto-restart backoff and signals the
// running agent to exit. Returns when the running attempt has
// finished or ctx is cancelled. A no-op if the agent isn't in the
// pool. After Stop the supervisor exits cleanly without spawning new
// attempts; a subsequent Pool.Start re-arms the auto-restart logic
// from scratch.
func (p *Pool) Stop(ctx context.Context, id uuid.UUID) error {
	p.mu.Lock()
	pa, ok := p.agents[id]
	p.mu.Unlock()

	if !ok {
		return nil
	}

	if pa.retryCancel != nil {
		pa.retryCancel()
	}

	return pa.agent.Stop(ctx)
}

// Remove cancels any pending auto-restart, stops the running attempt,
// waits for the supervisor to exit, removes the agent's on-disk state,
// and drops it from the pool.
func (p *Pool) Remove(ctx context.Context, id uuid.UUID) error {
	p.mu.Lock()
	pa, ok := p.agents[id]
	if ok {
		delete(p.agents, id)
	}
	p.mu.Unlock()

	if !ok {
		return nil
	}

	if pa.retryCancel != nil {
		pa.retryCancel()
	}

	_ = pa.agent.Stop(ctx)

	select {
	case <-pa.done:
	case <-ctx.Done():
		return ctx.Err()
	}

	return pa.agent.Remove(ctx)
}

// createAgent routes through the system provider's CreateWithOptions
// when available (so bind-mounts + log files + other per-instance
// AgentOption values take effect), and falls back to the stock
// executor.Provider.Create for any other provider type — keeping the
// abstract executor.Provider interface untouched.
func (p *Pool) createAgent(
	ctx context.Context, id uuid.UUID, ref spec.Reference,
	agentOpts []system.AgentOption, overrides []spec.Override,
) (agentpkg.Agent, error) {
	if sp, ok := p.provider.(*system.Provider); ok {
		return sp.CreateWithOptions(ctx, id, ref, agentOpts, overrides...)
	}

	return p.provider.Create(ctx, id, ref, overrides...)
}

func (p *Pool) runNew(id uuid.UUID, ref spec.Reference, agentOpts []system.AgentOption, overrides []spec.Override) {
	rootCtx := p.rootContext()

	// createAgent is fast (no subprocess); take the sem only for the
	// duration of the create so it doesn't block the supervisor's
	// per-attempt sem acquisition below.
	select {
	case p.sem <- struct{}{}:
	case <-rootCtx.Done():
		return
	}

	a, err := p.createAgent(rootCtx, id, ref, agentOpts, overrides)
	<-p.sem

	if err != nil {
		p.logger.Error("pool: provider.Create failed",
			zap.String("id", id.String()), zap.String("ref", ref.String()), zap.Error(err))

		return
	}

	done := make(chan struct{})
	retryCtx, retryCancel := context.WithCancel(rootCtx)

	p.mu.Lock()
	p.agents[id] = &pooledAgent{agent: a, done: done, retryCancel: retryCancel}
	p.mu.Unlock()

	defer close(done)
	defer retryCancel()

	p.runLoop(id, ref.String(), a, retryCtx, true)
}

func (p *Pool) runExisting(id uuid.UUID, a agentpkg.Agent, done chan struct{}) {
	rootCtx := p.rootContext()
	retryCtx, retryCancel := context.WithCancel(rootCtx)

	p.mu.Lock()
	if pa, ok := p.agents[id]; ok {
		pa.retryCancel = retryCancel
	}
	p.mu.Unlock()

	defer close(done)
	defer retryCancel()

	p.runLoop(id, "", a, retryCtx, false)
}

// runLoop drives the attempt-and-backoff loop for a single agent.
// First attempt of a fresh-create supervisor calls Run (materialise +
// serve); every subsequent attempt — and all attempts on a restart
// supervisor — calls Start (re-resolve + serve on the existing
// chroot). The loop exits when:
//   - retryCtx is cancelled (Pool.Stop / Remove / daemon shutdown).
//   - the attempt returns and status isn't an error: clean Stopped
//     after manual stop, runtime crash, or successful exit.
//
// Auto-restart is scoped to init/pull/model errors — failures that
// happened *before* the runtime subprocess started. A subprocess that
// crashed mid-run lands in Stopped (via the deferred status set in
// Agent.Run) and exits the supervisor; recovering from that needs a
// separate health-check mechanism (out of scope).
//
// per-attempt call — placing it after id/ref/a keeps the (id, status,
// agent, scope) ordering that reads naturally.
//
//nolint:revive // retryCtx scopes the supervisor lifetime, not the
func (p *Pool) runLoop(
	id uuid.UUID, ref string, a agentpkg.Agent,
	retryCtx context.Context, freshRun bool,
) {
	for attempt := 0; ; attempt++ {
		select {
		case p.sem <- struct{}{}:
		case <-retryCtx.Done():
			return
		}

		attemptCtx, cancelAttempt := context.WithCancel(retryCtx)

		var runErr error
		if freshRun && attempt == 0 {
			runErr = a.Run(attemptCtx)
		} else {
			runErr = a.Start(attemptCtx)
		}

		cancelAttempt()
		<-p.sem

		status := a.Status()

		if runErr == nil || !isErrorStatus(status) {
			return
		}

		delay := p.backoffDelay(attempt)

		p.logger.Warn("pool: agent failed; scheduling restart",
			zap.String("id", id.String()), zap.String("ref", ref),
			zap.String("status", status.String()),
			zap.Int("attempt", attempt+1),
			zap.Duration("delay", delay),
			zap.Error(runErr))

		p.writeFailureLog(id, status.String(), runErr)

		select {
		case <-retryCtx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// backoffDelay returns the sleep duration before the (attempt+1)th
// retry. Schedule: base, 2*base, 4*base, …, capped. attempt is
// zero-indexed: attempt=0 means "after the first failure, before the
// second try".
func (p *Pool) backoffDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}

	// Detect overflow: shifting past the bit-width of int64 wraps to
	// zero or negative, which would mean "no delay" — exactly the
	// opposite of what we want. Fall through to the cap instead.
	if attempt >= 32 {
		return p.backoffCap
	}

	d := p.backoffBase << attempt
	if d <= 0 || d > p.backoffCap {
		return p.backoffCap
	}

	return d
}

// isErrorStatus says whether the agent's lifecycle status reflects an
// initialization-time failure that auto-restart should retry.
// StatusStopped (graceful exit / manual stop / runtime crash) is
// excluded — a crashed runtime needs different handling than re-running
// materialize.
func isErrorStatus(s agentpkg.Status) bool {
	switch s {
	case agentpkg.StatusInitError, agentpkg.StatusPullError, agentpkg.StatusModelError:
		return true
	case agentpkg.StatusCreated, agentpkg.StatusRunning, agentpkg.StatusStopped,
		agentpkg.StatusRemoving, agentpkg.StatusRemoved:
		return false
	default:
		return false
	}
}

// writeFailureLog appends a single timestamped line summarising a
// Run/Start failure to <logDir>/<id>.log so `otters logs` surfaces
// the cause even when the runtime subprocess never started. No-op
// when logDir is unset (production wires it; unit tests typically don't).
// errors.Join introduces newlines between sentinel + cause; we collapse
// them with "; " so each failure is one log line.
func (p *Pool) writeFailureLog(id uuid.UUID, status string, runErr error) {
	if p.logDir == "" || runErr == nil {
		return
	}

	path := filepath.Join(p.logDir, id.String()+".log")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		p.logger.Warn("pool: writeFailureLog open failed",
			zap.String("id", id.String()), zap.Error(err))

		return
	}
	defer func() { _ = f.Close() }()

	msg := strings.ReplaceAll(runErr.Error(), "\n", "; ")
	ts := time.Now().UTC().Format(time.RFC3339)

	if _, werr := fmt.Fprintf(f, "[%s] %s: %s\n", ts, status, msg); werr != nil {
		p.logger.Warn("pool: writeFailureLog write failed",
			zap.String("id", id.String()), zap.Error(werr))
	}
}

// rootContext returns the pool's root context. Callers must only
// reach here after Init; the nil check is a guard against misuse,
// not a legitimate runtime branch.
func (p *Pool) rootContext() context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.rootCtx == nil {
		panic("pool: runNew/runExisting called before Init")
	}

	return p.rootCtx
}

func (p *Pool) stopAll(parent context.Context) error {
	p.mu.Lock()
	agents := make([]*pooledAgent, 0, len(p.agents))
	for _, pa := range p.agents {
		agents = append(agents, pa)
	}
	p.mu.Unlock()

	// parent's cancellation is the trigger that brought us here; we
	// need a fresh deadline for the drain itself. WithoutCancel keeps
	// any context values (logger, trace IDs) without inheriting the
	// already-tripped deadline.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer cancel()

	for _, pa := range agents {
		_ = pa.agent.Stop(ctx)
	}

	for _, pa := range agents {
		select {
		case <-pa.done:
		case <-ctx.Done():
		}
	}

	return nil
}
