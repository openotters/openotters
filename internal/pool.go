package internal

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	agentpkg "github.com/openotters/agentfile/agent"
	"github.com/openotters/agentfile/agent/system"
	"github.com/openotters/agentfile/spec"
)

const defaultMaxConcurrent = 4

// Pool manages a set of agents bound to a Provider. Adds are fire-and-forget:
// each Add spawns a goroutine that creates the agent via Provider and invokes
// Run. A semaphore bounds the number of concurrently running agents; extra
// adds block on the semaphore until a slot frees.
type Pool struct {
	provider agentpkg.Provider
	logger   *zap.Logger
	sem      chan struct{}

	mu     sync.Mutex
	agents map[uuid.UUID]*pooledAgent

	rootCtx    context.Context
	rootCancel context.CancelFunc
	started    bool
}

type pooledAgent struct {
	agent agentpkg.Agent
	done  chan struct{}
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

// NewPool returns a Pool that creates agents via provider.
func NewPool(provider agentpkg.Provider, opts ...PoolOption) *Pool {
	p := &Pool{
		provider: provider,
		logger:   zap.NewNop(),
		agents:   make(map[uuid.UUID]*pooledAgent),
		sem:      make(chan struct{}, defaultMaxConcurrent),
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

// Start re-runs an existing agent that was previously stopped. Non-blocking:
// spawns a goroutine that re-acquires the semaphore and invokes the agent's
// Start method. Errors during the restart surface through the pool logger
// and the agent's observers.
func (p *Pool) Start(id uuid.UUID) error {
	p.mu.Lock()
	pa, ok := p.agents[id]
	if !ok {
		p.mu.Unlock()

		return fmt.Errorf("agent %s not in pool", id)
	}

	done := make(chan struct{})
	pa.done = done
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

// Stop signals the agent to exit and waits for Run to return or ctx to cancel.
func (p *Pool) Stop(ctx context.Context, id uuid.UUID) error {
	p.mu.Lock()
	pa, ok := p.agents[id]
	p.mu.Unlock()

	if !ok {
		return nil
	}

	return pa.agent.Stop(ctx)
}

// Remove stops the agent, waits for Run to return, removes its on-disk state,
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
// agent.Provider.Create for any other provider type — keeping the
// abstract agent.Provider interface untouched.
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

	select {
	case p.sem <- struct{}{}:
	case <-rootCtx.Done():
		return
	}
	defer func() { <-p.sem }()

	ctx, cancel := context.WithCancel(rootCtx)
	defer cancel()

	a, err := p.createAgent(ctx, id, ref, agentOpts, overrides)
	if err != nil {
		p.logger.Error("pool: provider.Create failed",
			zap.String("id", id.String()), zap.String("ref", ref.String()), zap.Error(err))

		return
	}

	done := make(chan struct{})

	p.mu.Lock()
	p.agents[id] = &pooledAgent{agent: a, done: done}
	p.mu.Unlock()

	defer close(done)

	if runErr := a.Run(ctx); runErr != nil {
		p.logger.Warn("pool: agent.Run returned with error",
			zap.String("id", id.String()), zap.String("ref", ref.String()),
			zap.String("status", a.Status().String()), zap.Error(runErr))
	}
}

func (p *Pool) runExisting(id uuid.UUID, a agentpkg.Agent, done chan struct{}) {
	rootCtx := p.rootContext()

	select {
	case p.sem <- struct{}{}:
	case <-rootCtx.Done():
		close(done)

		return
	}
	defer func() { <-p.sem }()

	ctx, cancel := context.WithCancel(rootCtx)
	defer cancel()

	defer close(done)

	if runErr := a.Start(ctx); runErr != nil {
		p.logger.Warn("pool: agent.Start returned with error",
			zap.String("id", id.String()),
			zap.String("status", a.Status().String()), zap.Error(runErr))
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
