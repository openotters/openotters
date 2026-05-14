package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v6"
	"github.com/go-git/go-billy/v6/memfs"
	"github.com/go-git/go-billy/v6/osfs"
	"github.com/google/uuid"
	mobyclient "github.com/moby/moby/client"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"go.uber.org/zap"
	"oras.land/oras-go/v2"
	orasmem "oras.land/oras-go/v2/content/memory"

	agentbuild "github.com/openotters/agentfile/build"
	agentpkg "github.com/openotters/agentfile/executor"
	"github.com/openotters/agentfile/executor/docker"
	"github.com/openotters/agentfile/executor/system"
	"github.com/openotters/agentfile/export"
	agentoci "github.com/openotters/agentfile/oci"
	"github.com/openotters/agentfile/resolve"
	"github.com/openotters/agentfile/spec"
	afstore "github.com/openotters/agentfile/store"
	"github.com/openotters/bin/pkg/bin"
	daemonv1 "github.com/openotters/openotters/api/v1"
	"github.com/openotters/openotters/internal/asyncjobs"
	"github.com/openotters/openotters/internal/auth"
)

const (
	// Agent status strings — mirrors agentfile/executor.Status.String().
	// The daemon persists these in the agents table and emits them over
	// the wire via AgentInfo.status. Keep the values in sync.
	statusPulling  = "pulling"
	statusStarting = "starting"
	statusReady    = "ready"
	statusWorking  = "working"
	statusStopped  = "stopped"
	statusFailed   = "failed"
	statusRemoving = "removing"
	statusRemoved  = "removed"
	defaultTag     = "latest"

	// executorDocker is the cfg/daemon `executor` value that
	// switches the storage + runtime backend to Docker. The
	// default ("system") leaves things as host-subprocess +
	// embedded oras registry.
	executorDocker = "docker"
)

type managedAgent struct {
	id        uuid.UUID
	name      string
	agentName string
	model     string
	tag       string
	status    string
	createdAt time.Time
	mounts    []agentpkg.Mount
	// envOverrides are the per-run ENV values supplied at CreateAgent
	// time (CLI `-e KEY=VAL` flags or the dashboard's run-from-image
	// dialog). Persisted to daemon.db alongside the agent so they
	// survive daemon restarts; re-applied as spec.WithExtraEnvs
	// overrides on Restore.
	envOverrides []*spec.Env
	// labels — see api/v1/daemon.proto's "labels (shared semantics)"
	// comment for reserved io.openotters.* keys. Persisted alongside
	// the agent and surfaced on every ListAgents read.
	labels map[string]string

	// token is the agent's JWT — minted at CreateAgent, persisted
	// via SaveAgent, injected into the spawn env as
	// OTTERS_AGENT_TOKEN so the runtime can authenticate to the
	// daemon's TCP endpoint. tokenJTI is the JWT's `jti` claim,
	// used by Remove to revoke the token.
	token    string
	tokenJTI string

	// Image / runtime / tools metadata captured at create time.
	// Populated for agents created since the daemon started; left
	// empty on restored agents (would require a state-store migration
	// to persist).
	imageDigest   string
	runtimeRef    string
	runtimeDigest string
	tools         []agentTool
}

// agentTool mirrors daemonv1.AgentTool but stays internal so the
// in-memory state doesn't pin a proto type. Translated to the wire
// shape inside ListAgents.
type agentTool struct {
	Name        string
	Ref         string
	Digest      string
	Description string
}

// mountsFromPersisted translates the SQLite JSON form back into the
// agentpkg.Mount struct the provider consumes. Inverse of the
// conversion inside CreateAgent.
func mountsFromPersisted(pms []persistedMount) []agentpkg.Mount {
	if len(pms) == 0 {
		return nil
	}

	out := make([]agentpkg.Mount, 0, len(pms))
	for _, pm := range pms {
		out = append(out, agentpkg.Mount{
			Host:        pm.Host,
			Target:      pm.Target,
			Description: pm.Description,
			ReadOnly:    pm.ReadOnly,
		})
	}

	return out
}

// mountsToPersisted is the write-side counterpart used when
// persisting a freshly-created agent's mounts to the state store.
func mountsToPersisted(ms []agentpkg.Mount) []persistedMount {
	if len(ms) == 0 {
		return nil
	}

	out := make([]persistedMount, 0, len(ms))
	for _, m := range ms {
		out = append(out, persistedMount{
			Host:        m.Host,
			Target:      m.Target,
			Description: m.Description,
			ReadOnly:    m.ReadOnly,
		})
	}

	return out
}

// envsToPersisted shrinks spec.Env to its persistable shape — the
// state store only needs key + value (the description / required flag
// come from the agent's Agentfile, not from the operator's overrides).
func envsToPersisted(es []*spec.Env) []persistedEnv {
	if len(es) == 0 {
		return nil
	}

	out := make([]persistedEnv, 0, len(es))
	for _, e := range es {
		if e == nil || e.Key == "" {
			continue
		}
		out = append(out, persistedEnv{Key: e.Key, Value: e.Value})
	}

	return out
}

// envsFromPersisted is the read-side counterpart for Restore.
func envsFromPersisted(pes []persistedEnv) []*spec.Env {
	if len(pes) == 0 {
		return nil
	}

	out := make([]*spec.Env, 0, len(pes))
	for _, p := range pes {
		if p.Key == "" {
			continue
		}
		out = append(out, &spec.Env{Key: p.Key, Value: p.Value})
	}

	return out
}

// reservedMountPrefixes are chroot paths the agent's own tooling
// owns. Refusing mounts against them prevents a user from shadowing
// the runtime binary, the staged context/data layers, or the log
// directory — all of which would break materialization or hide
// security-relevant state from the agent.
//
//nolint:gochecknoglobals // immutable allow-list, used as a constant
var reservedMountPrefixes = []string{
	"/etc/context",
	"/etc/data",
	"/usr/bin",
	"/usr/local/bin",
	"/var",
}

// validateMounts translates an incoming slice of daemonv1.Mount into
// the internal agentpkg.Mount form, rejecting anything that would leave
// the daemon in a bad state: relative/empty paths, missing host
// files, reserved target prefixes, duplicate targets. The client is
// expected to have resolved `~`/`$PWD`/relative host paths already.
func validateMounts(in []*daemonv1.Mount) ([]agentpkg.Mount, error) {
	if len(in) == 0 {
		return nil, nil
	}

	out := make([]agentpkg.Mount, 0, len(in))
	seen := make(map[string]struct{}, len(in))

	for _, m := range in {
		host := m.GetHost()
		if !filepath.IsAbs(host) {
			return nil, fmt.Errorf("mount host path must be absolute: %q", host)
		}

		if _, err := os.Stat(host); err != nil {
			return nil, fmt.Errorf("mount host %s: %w", host, err)
		}

		target := filepath.Clean(m.GetTarget())
		if target == "." || target == "/" || !strings.HasPrefix(target, "/") {
			return nil, fmt.Errorf("mount target must be an absolute chroot path: %q", m.GetTarget())
		}

		if strings.Contains(target, "/..") {
			return nil, fmt.Errorf("mount target %q cannot traverse (..)", m.GetTarget())
		}

		for _, reserved := range reservedMountPrefixes {
			if target == reserved || strings.HasPrefix(target, reserved+"/") {
				return nil, fmt.Errorf("mount target %q collides with reserved prefix %s", target, reserved)
			}
		}

		if _, dup := seen[target]; dup {
			return nil, fmt.Errorf("duplicate mount target %q", target)
		}

		seen[target] = struct{}{}

		out = append(out, agentpkg.Mount{
			Host:        host,
			Target:      target,
			Description: m.GetDescription(),
			ReadOnly:    m.GetReadOnly(),
		})
	}

	return out, nil
}

// mountsForSpec converts the daemon's executor.Mount slice to the
// spec.Mount form consumed by spec.WithMounts. The override pipes
// the mounts through Agent.RuntimeMounts so both executor backends
// pick them up at Create time without needing a separate per-call
// channel — fixing the docker side which previously only honoured
// provider-level docker.WithMounts.
func mountsForSpec(mounts []agentpkg.Mount) []*spec.Mount {
	if len(mounts) == 0 {
		return nil
	}

	out := make([]*spec.Mount, 0, len(mounts))
	for _, m := range mounts {
		out = append(out, &spec.Mount{
			Host:        m.Host,
			Target:      m.Target,
			Description: m.Description,
			ReadOnly:    m.ReadOnly,
		})
	}

	return out
}

// envsFromRequest converts wire-form EnvOverride entries to the
// agentfile spec.Env shape consumed by spec.WithExtraEnvs. Empty
// keys are skipped silently — the CLI flag parser is responsible
// for catching malformed `KEY=VALUE` strings.
func envsFromRequest(in []*daemonv1.EnvOverride) []*spec.Env {
	if len(in) == 0 {
		return nil
	}

	out := make([]*spec.Env, 0, len(in))
	for _, e := range in {
		if e == nil || e.GetKey() == "" {
			continue
		}
		out = append(out, &spec.Env{Key: e.GetKey(), Value: e.GetValue()})
	}

	return out
}

// mountsToProto converts agentpkg.Mount slices to the protobuf form
// returned by ListAgents so the CLI `otters agent inspect` / `ps -v`
// can surface them.
func mountsToProto(ms []agentpkg.Mount) []*daemonv1.Mount {
	if len(ms) == 0 {
		return nil
	}

	out := make([]*daemonv1.Mount, 0, len(ms))
	for _, m := range ms {
		out = append(out, &daemonv1.Mount{
			Host:        m.Host,
			Target:      m.Target,
			Description: m.Description,
			ReadOnly:    m.ReadOnly,
		})
	}

	return out
}

type Daemon struct {
	pool      *Pool
	providers *ProviderRegistry
	registry  *EmbeddedRegistry
	state     *StateStore
	logger    *zap.Logger
	executor  string // "system" or "docker"; surfaced by Info().
	// storeFor lets the daemon resolve a per-ref OCI store (docker
	// image store under the docker executor, embedded HTTP registry
	// under the system executor). Used by DescribeImage to reach the
	// agent's config blob when the embedded registry doesn't carry
	// the image (docker-stored agents). Stored here rather than
	// reconstructed because docker.NewStore would otherwise open a
	// fresh moby client on every call.
	storeFor   func(spec.Reference) oras.ReadOnlyTarget
	logDir     string
	agentsDir  string
	dataDir    string
	runtime    string
	socket     string
	signingKey []byte // HMAC key for agent token issuance
	publicURL  string // TCP URL agents reach the daemon at (docker only)
	version    string
	commit     string
	buildDate  string
	catwalk    *catwalkCatalogue

	// Operator-tunable knobs surfaced by Info() for the dashboard.
	// `shutdownTimeout` is informational here — serve.go owns the
	// actual graceful-shutdown goroutine and reads its CLI flag
	// directly; storing it on the daemon keeps GetInfo as the single
	// source of truth for the UI.
	maxConcurrent   int
	backoffBase     time.Duration
	backoffCap      time.Duration
	shutdownTimeout time.Duration

	agents map[string]*managedAgent

	// asyncJobs dispatches BIN jobs against an agent's spawn env;
	// completions land in the agent's session as synthetic turns.
	// Constructed in NewDaemon, replayed on Boot, drained on shutdown.
	asyncJobs *asyncjobs.Pool
}

// DaemonOption configures the Daemon at construction time.
type DaemonOption func(*daemonConfig)

type daemonConfig struct {
	localRuntime    string
	socket          string
	publicURL       string
	signingKey      []byte
	version         string
	commit          string
	buildDate       string
	maxConcurrent   int
	backoffBase     time.Duration
	backoffCap      time.Duration
	shutdownTimeout time.Duration
	executor        string // "system" (default) or "docker"
}

// WithLocalRuntime overrides the runtime binary with a local filesystem
// path — bypasses the OCI pull of ghcr.io/openotters/runtime:latest.
// Useful during development when the published image lags local changes.
func WithLocalRuntime(path string) DaemonOption {
	return func(c *daemonConfig) { c.localRuntime = path }
}

// WithSocket records the unix socket the daemon is served on. Purely
// informational — used by `otters info` to report the listen path.
func WithSocket(path string) DaemonOption {
	return func(c *daemonConfig) { c.socket = path }
}

// WithBuildInfo stamps the daemon's build coordinates so `otters info`
// can surface the running version/commit/date. Populated from ldflags
// in cmd/ottersd/main.go.
func WithBuildInfo(version, commit, date string) DaemonOption {
	return func(c *daemonConfig) {
		c.version = version
		c.commit = commit
		c.buildDate = date
	}
}

// WithSigningKey sets the HMAC key the daemon uses to mint per-agent
// JWTs at CreateAgent. Same key is used by the JWT interceptor on
// the TCP listener — so agent tokens validate against the same
// secret that issues them.
func WithSigningKey(key []byte) DaemonOption {
	return func(c *daemonConfig) { c.signingKey = key }
}

// WithPublicURL sets the TCP URL the daemon binds (e.g.
// http://127.0.0.1:5500). Docker-executor agents need this to dial
// the daemon from inside their container — system agents go through
// the unix socket directly, so the URL is unused for them. The
// value is rewritten 127.0.0.1 → host.docker.internal at agent
// spawn time so the same URL works on macOS Docker Desktop and
// Linux Docker (with an ExtraHosts mapping).
func WithPublicURL(url string) DaemonOption {
	return func(c *daemonConfig) { c.publicURL = url }
}

// WithPoolMaxConcurrent caps the number of agents the underlying Pool
// will run simultaneously. Forwarded to pool.WithMaxConcurrent only
// when n > 0; a zero leaves the pool's own default in place.
func WithPoolMaxConcurrent(n int) DaemonOption {
	return func(c *daemonConfig) { c.maxConcurrent = n }
}

// WithPoolBackoffBase overrides the auto-restart base delay used by
// the Pool's supervisor loop. Forwarded only when d > 0.
func WithPoolBackoffBase(d time.Duration) DaemonOption {
	return func(c *daemonConfig) { c.backoffBase = d }
}

// WithPoolBackoffCap caps the maximum backoff between auto-restart
// attempts in the Pool's supervisor loop. Forwarded only when d > 0.
func WithPoolBackoffCap(d time.Duration) DaemonOption {
	return func(c *daemonConfig) { c.backoffCap = d }
}

// WithShutdownTimeout records the graceful-shutdown deadline applied
// to in-flight HTTP / Connect requests when the daemon receives
// SIGINT. Display-only on the daemon itself — serve.go owns the
// actual shutdown goroutine and reads its own flag — but keeping it
// here lets GetInfo render it on the dashboard alongside the other
// pool knobs.
func WithShutdownTimeout(d time.Duration) DaemonOption {
	return func(c *daemonConfig) { c.shutdownTimeout = d }
}

// WithExecutor selects the agent runtime backend: "system" (default,
// host subprocess) or "docker" (each agent in a container). Empty
// string keeps the default. The docker backend requires Docker
// Engine ≥ 25 with the containerd snapshotter enabled; failures
// surface at NewDaemon time with a clear error.
func WithExecutor(name string) DaemonOption {
	return func(c *daemonConfig) {
		if name != "" {
			c.executor = name
		}
	}
}

// buildStoreFor returns the per-ref oras.ReadOnlyTarget closure
// the executor uses to load agent OCI artifacts. The docker
// executor returns a docker.Store backed by the Docker daemon (so
// builds and reads share the same image store); the system
// executor — and any future backend — falls back to the embedded
// registry's per-ref HTTP target.
//
// docker.Store is stateful: a fresh one per ref ensures the load
// path's hydrate cache only carries that agent's blobs. The
// closure wraps a single shared docker client (lazily created on
// first call) so we don't pay the connection negotiation cost per
// ref.
func buildStoreFor(
	cfg *daemonConfig, _ *EmbeddedRegistry, logger *zap.Logger,
) func(spec.Reference) oras.ReadOnlyTarget {
	if cfg.executor == executorDocker {
		var (
			cliMu sync.Mutex
			cli   *mobyclient.Client
		)

		return func(ref spec.Reference) oras.ReadOnlyTarget {
			cliMu.Lock()
			if cli == nil {
				c, err := docker.NewClient()
				if err != nil {
					cliMu.Unlock()
					logger.Error("docker store: open client", zap.String("ref", ref.String()), zap.Error(err))
					return erroringTarget{err: err}
				}
				cli = c
			}
			cliMu.Unlock()

			return docker.NewStore(cli)
		}
	}

	return func(ref spec.Reference) oras.ReadOnlyTarget {
		t, err := newRegistryTarget(ref)
		if err != nil {
			logger.Error("registry target", zap.String("ref", ref.String()), zap.Error(err))

			return erroringTarget{err: err}
		}

		return t
	}
}

// buildExecutorProvider returns the executor.Provider implementation
// selected by cfg.executor. Defaulting "" or "system" gives the
// host-subprocess backend; "docker" gives the container backend.
// Pulled out of NewDaemon so the latter stays under funlen's
// 100-line limit while we add backend choices.
func buildExecutorProvider(
	cfg *daemonConfig,
	root billy.Filesystem,
	storeFor func(spec.Reference) oras.ReadOnlyTarget,
	reg *EmbeddedRegistry,
	logDir string,
	providers *ProviderRegistry,
) agentpkg.Provider {
	if cfg.executor == executorDocker {
		// No newCachingBinPuller here on purpose: the docker
		// executor pulls BIN images via cli.ImagePull straight
		// into Docker's image cache, and Docker's own content
		// store dedupes layers across agents. Mirroring through
		// the embedded oras-go registry would just be a second
		// copy with no benefit.
		dockerOpts := []docker.ProviderOption{docker.WithLogDir(logDir)}

		if providers != nil {
			dockerOpts = append(dockerOpts, docker.WithModelResolver(providers.Resolve))
		}

		if reg != nil {
			dockerOpts = append(dockerOpts, docker.WithUsageFetcher(newCachingUsageFetcher(reg.Addr())))
		}

		dp, err := docker.NewProvider(
			root,
			docker.StoreFor(storeFor),
			dockerOpts...,
		)
		if err != nil {
			// Surface to operator at startup. Common cases:
			// daemon unreachable, engine < 25, snapshotter
			// disabled. Panic is appropriate here — daemon can't
			// usefully run with the requested executor down.
			panic(fmt.Sprintf("docker executor: %v", err))
		}

		return dp
	}

	opts := []system.ProviderOption{system.WithLogDir(logDir)}

	if cfg.localRuntime != "" {
		opts = append(opts, system.WithLocalRuntime(cfg.localRuntime))
	}

	if reg != nil {
		opts = append(opts,
			system.WithPuller(newCachingBinPuller(reg.Addr())),
			system.WithUsageFetcher(newCachingUsageFetcher(reg.Addr())),
			system.WithRegistryAddr(reg.Addr()),
			system.WithRegistryCreatedAt(reg.ManifestCreatedAt),
		)
	}

	return system.NewProvider(root, storeFor, opts...)
}

func NewDaemon(
	providers *ProviderRegistry, reg *EmbeddedRegistry, state *StateStore,
	logger *zap.Logger, opts ...DaemonOption,
) *Daemon {
	cfg := daemonConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".otters")
	agentsDir := filepath.Join(dataDir, "agents")
	logDir := filepath.Join(dataDir, "logs")

	_ = os.MkdirAll(agentsDir, 0o755)
	_ = os.MkdirAll(logDir, 0o755)

	root := osfs.New(agentsDir)

	// storeFor is the per-ref oras.ReadOnlyTarget the executor uses
	// to load an agent's OCI artifact. The system executor reads
	// from the daemon's embedded oras registry; the docker executor
	// reads from the Docker daemon's image store via docker.Store.
	// Building the closure here so each branch picks the right
	// backend without leaking that detail into pool / executor
	// code.
	storeFor := buildStoreFor(&cfg, reg, logger)

	provider := buildExecutorProvider(&cfg, root, storeFor, reg, logDir, providers)

	daemonLogger := logger.Named("daemon")

	executorName := cfg.executor
	if executorName == "" {
		executorName = "system"
	}
	daemonLogger.Info("executor selected", zap.String("backend", executorName))

	poolOpts := []PoolOption{
		WithLogger(daemonLogger.Named("pool")),
		WithLogDir(logDir),
	}

	if cfg.maxConcurrent > 0 {
		poolOpts = append(poolOpts, WithMaxConcurrent(cfg.maxConcurrent))
	}

	if cfg.backoffBase > 0 {
		poolOpts = append(poolOpts, WithBackoffBase(cfg.backoffBase))
	}

	if cfg.backoffCap > 0 {
		poolOpts = append(poolOpts, WithBackoffCap(cfg.backoffCap))
	}

	d := &Daemon{
		pool:            NewPool(provider, poolOpts...),
		providers:       providers,
		registry:        reg,
		state:           state,
		logger:          daemonLogger,
		executor:        executorName,
		storeFor:        storeFor,
		logDir:          logDir,
		agentsDir:       agentsDir,
		dataDir:         dataDir,
		runtime:         cfg.localRuntime,
		socket:          cfg.socket,
		signingKey:      cfg.signingKey,
		publicURL:       cfg.publicURL,
		version:         cfg.version,
		commit:          cfg.commit,
		buildDate:       cfg.buildDate,
		maxConcurrent:   cfg.maxConcurrent,
		backoffBase:     cfg.backoffBase,
		backoffCap:      cfg.backoffCap,
		shutdownTimeout: cfg.shutdownTimeout,
		catwalk:         newCatwalkCatalogue(),
		agents:          make(map[string]*managedAgent),
	}

	// Async-jobs Pool: dispatcher + Store. The pool persists status
	// transitions to the row; observers (agent runtime, operator CLI,
	// UI) read via GetAsyncJob / ListAsyncJobs RPCs and decide on
	// their own watch strategy. The daemon itself never pushes
	// completion anywhere.
	d.asyncJobs = asyncjobs.NewPool(
		asyncjobs.NewStore(state.db),
		d, // daemon implements asyncjobs.AgentLookup via pool.Get
		daemonLogger,
	)

	return d
}

// agentReachableURL returns the URL form an agent's runtime should
// dial to reach this daemon. Two backends, two transports:
//
//   - system: agents are host subprocesses sharing the host
//     filesystem, so a unix socket bind-mount is unnecessary —
//     hand them the daemon's socket path directly as
//     unix://<path>. The chrooted runtime can dial it (chroot is
//     billy-rooted, not a real syscall chroot).
//   - docker on native Linux: bind-mount the daemon's unix socket
//     into the container. Clean, no TCP, no DNS games. The docker
//     executor sees the unix:// scheme, mounts the host socket at
//     the canonical in-container path, and rewrites OTTERSD_URL
//     accordingly.
//   - docker on Docker Desktop / Colima: bind-mounting unix sockets
//     through virtiofs fails ("operation not supported"). Fall
//     back to TCP via host.docker.internal — publicURL is the
//     daemon's bound TCP URL with 127.0.0.1 rewritten so the name
//     resolves from inside the container.
//
// Empty when the daemon doesn't have the relevant transport
// configured (e.g. docker with no socket AND no --http-addr).
// Agents created with empty URL silently lack daemon-callback
// capability.
func (d *Daemon) agentReachableURL() string {
	if d.executor == executorDocker {
		// Linux: prefer the unix-socket bind-mount path. The docker
		// executor handles the host-path → in-container-path rewrite.
		if runtime.GOOS == "linux" && d.socket != "" {
			return auth.SocketURL(d.socket)
		}

		if d.publicURL == "" {
			return ""
		}

		return strings.ReplaceAll(d.publicURL, "127.0.0.1", "host.docker.internal")
	}

	if d.socket == "" {
		return ""
	}

	return auth.SocketURL(d.socket)
}

// Get implements asyncjobs.AgentLookup so the async-jobs Pool can
// resolve a job's agent UUID to a running Agent. Returns false when
// the agent is stopped/removed/unknown.
func (d *Daemon) Get(id uuid.UUID) (agentpkg.Agent, bool) {
	return d.pool.Get(id)
}

// AsyncJobs exposes the async-jobs Pool to the gRPC handlers and to
// the serve loop's Boot / Shutdown calls.
func (d *Daemon) AsyncJobs() *asyncjobs.Pool { return d.asyncJobs }

// Info returns a snapshot of the daemon's runtime coordinates for
// display by `otters info`. Cheap — everything is in-memory.
func (d *Daemon) Info() DaemonInfo {
	running := 0

	for _, ma := range d.agents {
		if a, ok := d.pool.Get(ma.id); ok {
			// "Running" for the dashboard counter = ready OR working;
			// either way the agent is alive and serving traffic. All
			// other states (pulling/starting/stopped/failed/removing/
			// removed) don't count toward the running tally.
			switch a.Status() {
			case agentpkg.StatusReady, agentpkg.StatusWorking:
				running++
			case agentpkg.StatusPulling, agentpkg.StatusStarting,
				agentpkg.StatusStopped, agentpkg.StatusFailed,
				agentpkg.StatusRemoving, agentpkg.StatusRemoved:
				// not running
			}
		}
	}

	registryAddr := ""
	if d.registry != nil {
		registryAddr = d.registry.Addr()
	}

	providerCount := 0
	if d.providers != nil {
		providerCount = d.providers.Count()
	}

	return DaemonInfo{
		Executor:        d.executor,
		RegistryAddr:    registryAddr,
		SocketPath:      d.socket,
		LogDir:          d.logDir,
		AgentsDir:       d.agentsDir,
		DataDir:         d.dataDir,
		RuntimePath:     d.runtime,
		Version:         d.version,
		Commit:          d.commit,
		BuildDate:       d.buildDate,
		AgentsRunning:   running,
		AgentsTotal:     len(d.agents),
		Providers:       providerCount,
		MaxConcurrent:   d.maxConcurrent,
		BackoffBase:     d.backoffBase,
		BackoffCap:      d.backoffCap,
		ShutdownTimeout: d.shutdownTimeout,
	}
}

// DaemonInfo is a plain-data snapshot of the daemon's current
// configuration. Mirrored one-for-one into daemonv1.GetInfoResponse by
// the gRPC handler; kept as its own type so non-gRPC callers (tests,
// future local APIs) don't need to import the generated package.
type DaemonInfo struct {
	// Executor is the active agent backend ("system" / "docker").
	// Surfaced by Info so `otters info` and the dashboard can show
	// which backend is running.
	Executor        string
	RegistryAddr    string
	SocketPath      string
	LogDir          string
	AgentsDir       string
	DataDir         string
	RuntimePath     string
	Version         string
	Commit          string
	BuildDate       string
	AgentsRunning   int
	AgentsTotal     int
	Providers       int
	MaxConcurrent   int
	BackoffBase     time.Duration
	BackoffCap      time.Duration
	ShutdownTimeout time.Duration
}

// ModelInfo is one row of the daemon's provider catalogue — the
// shape returned by Daemon.Models and wired to daemonv1.Model by the
// gRPC handler. Ref is the Agentfile-compatible "<provider>/<name>"
// form so the CLI can present a paste-ready value alongside the
// structured columns. Enriched fields come from Catwalk (Charm's
// curated metadata DB) when the provider is recognised there;
// otherwise they stay zero.
type ModelInfo struct {
	Provider         string
	Name             string
	Ref              string
	DisplayName      string
	APIBase          string
	ContextWindow    int64
	DefaultMaxTokens int64
	CostInputPer1M   float64
	CostOutputPer1M  float64
	CanReason        bool
}

// Models returns every model reachable through the daemon's
// configured providers. Precedence:
//  1. Explicit `models:` list in providers.yaml — authoritative.
//  2. Catwalk (Charm's community-curated model DB) matched by
//     provider name — gives rich metadata (cost, context, reasoning).
//  3. A synthetic "<provider>/*" placeholder — fallback when Catwalk
//     doesn't know the provider (self-hosted, private, etc.).
func (d *Daemon) Models(ctx context.Context) []ModelInfo {
	if d.providers == nil {
		return nil
	}

	var out []ModelInfo

	d.providers.Each(func(p *ProviderConfig) {
		// Explicit config wins — authoritative and cheap.
		if len(p.Models) > 0 {
			for _, m := range p.Models {
				out = append(out, ModelInfo{
					Provider: p.Name,
					Name:     m,
					Ref:      p.Name + "/" + m,
					APIBase:  p.APIBase,
				})
			}

			return
		}

		// Ask Catwalk for the provider's model catalogue.
		models, err := d.catwalk.modelsFor(ctx, p.Name)
		if err != nil {
			d.logger.Warn("catwalk fetch failed",
				zap.String("provider", p.Name),
				zap.Error(err),
			)
		}

		if len(models) == 0 {
			out = append(out, ModelInfo{
				Provider: p.Name,
				Name:     "*",
				Ref:      p.Name + "/*",
				APIBase:  p.APIBase,
			})

			return
		}

		for _, m := range models {
			out = append(out, ModelInfo{
				Provider:         p.Name,
				Name:             m.ID,
				Ref:              p.Name + "/" + m.ID,
				DisplayName:      m.Name,
				APIBase:          p.APIBase,
				ContextWindow:    m.ContextWindow,
				DefaultMaxTokens: m.DefaultMaxTokens,
				CostInputPer1M:   m.CostPer1MIn,
				CostOutputPer1M:  m.CostPer1MOut,
				CanReason:        m.CanReason,
			})
		}
	})

	return out
}

// AgentLogs returns the tail of the runtime log file for the agent
// identified by ref. If tailLines > 0, return only the last N lines
// (wins over tailBytes). Otherwise if tailBytes > 0, return only the
// last N bytes. With both zero, the full file is returned.
func (d *Daemon) AgentLogs(ref string, tailBytes, tailLines int64) ([]byte, string, error) {
	ma, err := d.resolve(ref)
	if err != nil {
		return nil, "", err
	}

	path := filepath.Join(d.logDir, ma.id.String()+".log")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, path, nil
		}

		return nil, path, err
	}

	switch {
	case tailLines > 0:
		return tailByLines(data, tailLines), path, nil
	case tailBytes > 0 && int64(len(data)) > tailBytes:
		return data[int64(len(data))-tailBytes:], path, nil
	default:
		return data, path, nil
	}
}

// tailByLines returns the last n complete lines of data. A trailing
// newline is preserved. If data has fewer than n lines, the whole
// buffer comes back untouched.
func tailByLines(data []byte, n int64) []byte {
	if n <= 0 || len(data) == 0 {
		return data
	}

	// Ignore a trailing newline so "hello\n" counts as one line, not two.
	end := len(data)
	if data[end-1] == '\n' {
		end--
	}

	count := int64(0)

	for i := end - 1; i >= 0; i-- {
		if data[i] != '\n' {
			continue
		}

		count++
		if count == n {
			return data[i+1:]
		}
	}

	return data
}

// Run initializes the pool synchronously (so rootCtx is live before
// any Add / Restore lands) and then spawns the lifecycle goroutine
// that blocks until ctx is cancelled. Returns an error if Init fails
// (double-start); the wait goroutine logs its own errors.
func (d *Daemon) Run(ctx context.Context) error {
	if err := d.pool.Init(ctx); err != nil {
		return fmt.Errorf("starting pool: %w", err)
	}

	go func() {
		if err := d.pool.Wait(); err != nil {
			d.logger.Error("pool error", zap.Error(err))
		}
	}()

	return nil
}

// Start re-runs a previously-stopped agent. Non-blocking: the pool spawns
// a goroutine to handle the actual restart. Status transitions are visible
// via List once the runtime subprocess is back up.
func (d *Daemon) Start(ctx context.Context, ref string) error {
	ma, err := d.resolve(ref)
	if err != nil {
		return err
	}

	if startErr := d.pool.Start(ma.id); startErr != nil {
		return startErr
	}

	// pool.Start kicks off the supervisor goroutine; the agent will
	// transition through pulling → starting → ready via the executor +
	// the supervisor's readiness probe. We persist "starting" here so a
	// daemon restart mid-launch doesn't leave the row stuck at the old
	// terminal status; the supervisor's status-sync will overwrite it
	// once the executor publishes the next transition.
	ma.status = statusStarting

	if updateErr := d.state.UpdateStatus(ctx, ma.id.String(), statusStarting); updateErr != nil {
		d.logger.Warn("failed to persist start", zap.Error(updateErr))
	}

	d.logger.Info("agent started", zap.String("id", ma.id.String()), zap.String("name", ma.name))

	return nil
}

func (d *Daemon) RegistryAddr() string {
	if d.registry == nil {
		return ""
	}

	return d.registry.Addr()
}

func (d *Daemon) Restore(ctx context.Context) error {
	persisted, err := d.state.ListAgents(ctx)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	if len(persisted) == 0 {
		return nil
	}

	var restored int

	for _, pa := range persisted {
		id, parseErr := uuid.Parse(pa.ID)
		if parseErr != nil {
			d.logger.Warn("invalid agent ID, skipping", zap.String("id", pa.ID))

			continue
		}

		ref := d.resolveStoredTag(pa.Tag)

		var overrides []spec.Override
		if pa.Model != "" {
			overrides = append(overrides, spec.WithModel(pa.Model))
		}

		// pa.Runtime holds the effective runtime (operator override or
		// the Agentfile's baked default) — re-apply it so the runtime
		// override survives. No-op when the persisted value equals
		// the baked one; non-trivial when the operator chose a
		// different runtime.
		if pa.Runtime != "" {
			overrides = append(overrides, spec.WithRuntime(pa.Runtime))
		}

		mounts := mountsFromPersisted(pa.Mounts)
		envOverrides := envsFromPersisted(pa.Envs)

		d.agents[pa.ID] = &managedAgent{
			id:           id,
			name:         pa.Name,
			agentName:    pa.AgentName,
			model:        pa.Model,
			tag:          pa.Tag,
			status:       pa.Status,
			createdAt:    pa.CreatedAt,
			mounts:       mounts,
			envOverrides: envOverrides,
			labels:       pa.Labels,
			token:        pa.Token,
			tokenJTI:     pa.TokenJTI,
		}

		agentOpts := []system.AgentOption{
			system.WithModelResolver(d.providers.Resolve),
		}

		if len(mounts) > 0 {
			agentOpts = append(agentOpts, system.WithMounts(mounts))
		}

		if specMounts := mountsForSpec(mounts); len(specMounts) > 0 {
			overrides = append(overrides, spec.WithMounts(specMounts))
		}

		// Re-apply persisted ENV overrides so the operator's CLI
		// `-e KEY=VAL` flags survive daemon restarts.
		if len(envOverrides) > 0 {
			overrides = append(overrides, spec.WithExtraEnvs(envOverrides))
		}

		if pa.Status != statusStopped {
			d.pool.Add(id, ref, agentOpts, AgentExtras{
				DaemonURL:  d.agentReachableURL(),
				AgentToken: pa.Token,
			}, overrides...)

			// Restored agents go through Run() again on daemon boot —
			// pulling caches, materialising, spawning. The supervisor
			// will flip to ready / failed in the usual way. Persist
			// "starting" as the floor so the row reflects "we're
			// trying to bring this back up" rather than the stale
			// pre-shutdown status.
			d.agents[pa.ID].status = statusStarting
			restored++

			d.logger.Info("agent restored",
				zap.String("id", pa.ID),
				zap.String("name", pa.Name),
				zap.String("image", pa.Tag),
				zap.String("model", pa.Model),
			)
		} else {
			d.logger.Info("agent restored (stopped)",
				zap.String("id", pa.ID),
				zap.String("name", pa.Name),
				zap.String("image", pa.Tag),
			)
		}
	}

	d.logger.Info("agents restored", zap.Int("count", restored), zap.Int("total", len(persisted)))

	return nil
}

// resolveStoredTag turns a persisted tag (the path-only form, e.g.
// "reader:latest" or "ghcr.io/foo/agent:v1") into the live local ref
// by prepending the current embedded registry address. It also defends
// against legacy rows that stored the addr-prefixed form (including
// port-drifted doubles like "127.0.0.1:OLDPORT/reader:latest"): any
// leading "127.0.0.1:<port>/" segments are stripped first.
func (d *Daemon) resolveStoredTag(tag string) spec.Reference {
	tag = stripLoopbackPrefixes(tag)

	return d.localRef(tag)
}

// stripLoopbackPrefixes removes every leading "127.0.0.1:<port>/"
// component from a stored image tag. Agents persisted under previous
// daemon runs may carry one — or several, if restore re-prefixed — so
// loop until the tag no longer starts with a loopback host:port path.
func stripLoopbackPrefixes(tag string) string {
	const loopback = "127.0.0.1:"

	for strings.HasPrefix(tag, loopback) {
		slash := strings.Index(tag, "/")
		if slash < 0 {
			return tag
		}

		tag = tag[slash+1:]
	}

	return tag
}

// Build parses the Agentfile at req.AgentfilePath on the daemon host,
// runs the resolve → build → push pipeline into the embedded registry,
// and returns the resulting digest + tags. This moves all heavy build
// logic server-side; the CLI just ships the path.
func (d *Daemon) Build(
	ctx context.Context, req *daemonv1.BuildAgentRequest,
) (*daemonv1.BuildAgentResponse, error) {
	path := strings.TrimSpace(req.GetAgentfilePath())
	if path == "" {
		return nil, fmt.Errorf("agentfile_path is required")
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving agentfile path: %w", err)
	}

	if _, err = os.Stat(abs); err != nil {
		return nil, fmt.Errorf("agentfile %s: %w", abs, err)
	}

	source, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", abs, err)
	}

	af, err := spec.Parse(bytes.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", abs, err)
	}

	// CLI builds get an osfs rooted at the Agentfile's directory so
	// `ADD ./local-file` and `CONTEXT FROM file://` directives resolve
	// against the build context the user invoked us from.
	src := osfs.New(filepath.Dir(abs))

	return d.buildAgentfile(ctx, af, source, src, req.GetTags(), zap.String("path", abs))
}

// BuildFromBytes is the inline variant used by the web UI: the
// Agentfile content arrives in the request body and is parsed in
// memory — no temp file under ~/.otters/builds-tmp. The build context
// is an empty memfs, so ADD / file:// directives won't resolve here;
// agents that compose themselves from heredoc CONTEXT + registry BIN
// (the path the UI form generates) work fine.
func (d *Daemon) BuildFromBytes(
	ctx context.Context, content []byte, tags []string,
) (*daemonv1.BuildAgentResponse, error) {
	if len(content) == 0 {
		return nil, fmt.Errorf("agentfile content is empty")
	}

	af, err := spec.Parse(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("parsing inline agentfile: %w", err)
	}

	return d.buildAgentfile(ctx, af, content, memfs.New(), tags,
		zap.Int("inline_bytes", len(content)),
	)
}

// buildAgentfile is the shared resolve-build-push pipeline used by both
// Build (CLI / path) and BuildFromBytes (inline / UI). The custom
// parent fetcher rewrites bare refs to land in the embedded local
// registry first, with the upstream remote fetcher as the fallback —
// so an Agentfile with `FROM web-summarizer:latest` finds its parent
// inside the daemon's own registry instead of failing with
// "missing registry or repository".
func (d *Daemon) buildAgentfile(
	ctx context.Context,
	af *spec.Agentfile,
	source []byte,
	src billy.Filesystem,
	tags []string,
	extraLogField zap.Field,
) (*daemonv1.BuildAgentResponse, error) {
	resolved, err := resolve.Resolve(ctx, af, d.parentFetcher())
	if err != nil {
		return nil, fmt.Errorf("resolving: %w", err)
	}

	store := d.openBuildStore()

	built, err := agentbuild.Build(ctx, resolved, source, src, store)
	if err != nil {
		return nil, fmt.Errorf("building: %w", err)
	}

	if len(tags) == 0 {
		name := built.Reference.Name
		if name == "" {
			name = "agent"
		}

		tags = []string{name + ":" + spec.DefaultTag}
	}

	srcRef := built.Digest.String()

	pushed := make([]string, 0, len(tags))
	for _, tag := range tags {
		ref := d.refFor(tag)
		if pushErr := d.commitBuilt(ctx, store, srcRef, ref,
			built.Digest.String(), spec.AgentArtifactType); pushErr != nil {
			return nil, fmt.Errorf("pushing %s: %w", tag, pushErr)
		}

		pushed = append(pushed, tag)
	}

	d.logger.Info("agent built",
		extraLogField,
		zap.String("digest", built.Digest.String()),
		zap.Strings("tags", pushed),
	)

	return &daemonv1.BuildAgentResponse{
		Digest: built.Digest.String(),
		Tags:   pushed,
		Ref:    built.Reference.String(),
	}, nil
}

// parentFetcher returns a resolve.Fetcher that prefers the daemon's
// embedded registry for unqualified refs (e.g. `FROM hello:latest`),
// falling back to the standard remote fetcher for fully-qualified
// references like `ghcr.io/openotters/agents/foo:latest`. Without
// this, FROM directives that name a locally-built parent fail at
// agentfile/oci's NewRemoteRepository step with "missing registry or
// repository".
func (d *Daemon) parentFetcher() resolve.Fetcher {
	upstream := agentoci.AgentFetcher()

	return func(ctx context.Context, ref spec.Reference) (*spec.Agentfile, error) {
		if !spec.IsQualified(ref.Name) && d.registry != nil {
			return upstream(ctx, spec.QualifyWithDefault(ref, d.registry.Addr()))
		}

		return upstream(ctx, ref)
	}
}

// BuildTool builds a multi-arch tool OCI image from the per-platform
// binaries on the daemon host, then pushes it to the embedded registry
// under the requested tags. Mirrors `Build` (agent) but uses
// bintool/pkg/bin's BuildIndex for multi-arch packing.
func (d *Daemon) BuildTool(
	ctx context.Context, req *daemonv1.BuildToolImageRequest,
) (*daemonv1.BuildToolImageResponse, error) {
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	if len(req.GetPlatforms()) == 0 {
		return nil, fmt.Errorf("at least one platform is required")
	}

	platforms := make([]bin.PlatformBuild, 0, len(req.GetPlatforms()))

	for _, p := range req.GetPlatforms() {
		abs, absErr := filepath.Abs(p.GetBinPath())
		if absErr != nil {
			return nil, fmt.Errorf("resolving %s: %w", p.GetBinPath(), absErr)
		}

		if _, statErr := os.Stat(abs); statErr != nil {
			return nil, fmt.Errorf("binary %s: %w", abs, statErr)
		}

		platforms = append(platforms, bin.PlatformBuild{
			OS:      p.GetOs(),
			Arch:    p.GetArch(),
			BinPath: filepath.Base(abs),
			Src:     osfs.New(filepath.Dir(abs)),
		})
	}

	store := d.openBuildStore()

	dig, err := bin.BuildIndex(ctx, bin.BuildOptions{
		Name:        name,
		BinPath:     platforms[0].BinPath,
		Description: req.GetDescription(),
		Usage:       req.GetUsage(),
		Source:      req.GetSource(),
	}, platforms, store)
	if err != nil {
		return nil, fmt.Errorf("building: %w", err)
	}

	tags := req.GetTags()
	if len(tags) == 0 {
		tags = []string{name + ":" + spec.DefaultTag}
	}

	// bin.BuildIndex tags the index as "latest" in the store —
	// the system executor's pushImage resolves srcRef by tag, so
	// we hand it "latest". The docker executor resolves by digest
	// out of the staged blob map; commitBuilt special-cases that.
	pushed := make([]string, 0, len(tags))
	for _, tag := range tags {
		ref := d.refFor(tag)
		srcRef := "latest"
		if d.executor == executorDocker {
			srcRef = dig.String()
		}
		if pushErr := d.commitBuilt(ctx, store, srcRef, ref,
			dig.String(), spec.BinArtifactType); pushErr != nil {
			return nil, fmt.Errorf("pushing %s: %w", tag, pushErr)
		}

		pushed = append(pushed, tag)
	}

	d.logger.Info("tool built",
		zap.String("name", name),
		zap.String("digest", dig.String()),
		zap.Int("platforms", len(platforms)),
		zap.Strings("tags", pushed),
	)

	return &daemonv1.BuildToolImageResponse{
		Digest: dig.String(),
		Tags:   pushed,
		Ref:    name + ":" + spec.DefaultTag,
	}, nil
}

func (d *Daemon) Save(
	ctx context.Context, req *daemonv1.SaveAgentImageRequest,
) (*daemonv1.SaveAgentImageResponse, error) {
	store, digest, err := importArtifact(ctx, req.GetOciArtifact())
	if err != nil {
		return nil, fmt.Errorf("importing artifact: %w", err)
	}

	tags := req.GetTags()

	for _, tag := range tags {
		ref := d.localRef(tag)

		if pushErr := d.pushImage(ctx, store, digest, ref); pushErr != nil {
			return nil, fmt.Errorf("saving %s to local registry: %w", tag, pushErr)
		}
	}

	// Refresh the daemon's image cache for every saved tag so the
	// listing surfaces pick them up immediately.
	d.upsertImagesFromTags(ctx, tags, "")

	d.logger.Info("image saved", zap.String("digest", digest), zap.Strings("tags", tags))

	return &daemonv1.SaveAgentImageResponse{Digest: digest, Tags: tags}, nil
}

func (d *Daemon) Pull(
	ctx context.Context, req *daemonv1.PullRequest,
) (*daemonv1.PullResponse, error) {
	ref := spec.ParseReference(req.GetRef())

	if d.registry != nil && strings.HasPrefix(ref.Name, d.registry.Addr()) {
		return nil, fmt.Errorf("cannot pull a local reference %s", ref)
	}

	reg := d.pool.provider.Registry()

	if err := reg.PullRemote(ctx, req.GetRef()); err != nil {
		return nil, fmt.Errorf("pulling %s: %w", ref, err)
	}

	tag := ref.Tag
	if tag == "" {
		tag = defaultTag
	}

	tags := req.GetTags()
	if len(tags) == 0 {
		tags = []string{ref.Name + ":" + tag}
	}

	// Best-effort digest lookup via Inspect on the first tag — purely
	// informational on the response. Skip if Inspect isn't supported.
	var digest string

	if info, err := reg.Inspect(ctx, tags[0]); err == nil {
		digest = info.Digest
	}

	// Refresh the daemon's image cache for every pulled tag.
	d.upsertImagesFromTags(ctx, tags, "")

	d.logger.Info("image pulled", zap.String("ref", req.GetRef()), zap.Strings("tags", tags))

	return &daemonv1.PullResponse{Digest: digest, Tags: tags}, nil
}

func (d *Daemon) Push(
	ctx context.Context, req *daemonv1.PushRequest,
) (*daemonv1.PushResponse, error) {
	ref := req.GetRef()

	d.logger.Info("pushing to remote", zap.String("ref", ref))

	reg := d.pool.provider.Registry()

	if err := reg.PushRemote(ctx, ref, ref); err != nil {
		return nil, fmt.Errorf("pushing to %s: %w", ref, err)
	}

	// Best-effort digest lookup via Inspect.
	var digest string

	if info, err := reg.Inspect(ctx, ref); err == nil {
		digest = info.Digest
	}

	return &daemonv1.PushResponse{Digest: digest, Ref: ref}, nil
}

//nolint:funlen // sequential agent-creation flow reads more clearly straight-through
func (d *Daemon) CreateAgent(
	ctx context.Context,
	req *daemonv1.CreateAgentRequest,
) (*daemonv1.CreateAgentResponse, error) {
	// refFor qualifies the user's ref against the active executor
	// backend: the embedded registry for system, Docker's image
	// store (refs stay verbatim) for docker. Replaces the old
	// "no slash → unqualified" heuristic, which mis-classified
	// refs like `agents/foo:v1` (slash present, but still bare).
	ref := d.refFor(req.GetRef())

	target, err := d.openReadTarget(ref)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", ref, err)
	}

	_, af, loadErr := afstore.Load(ctx, target, ref)
	if loadErr != nil {
		return nil, fmt.Errorf("loading agentfile from %s: %w", req.GetRef(), loadErr)
	}

	if af.Agent == nil {
		return nil, fmt.Errorf("no agent defined in image %s", req.GetRef())
	}

	id := uuid.New()

	name := req.GetName()
	if name == "" {
		name = generateName()
	}

	if d.nameExists(name) {
		return nil, fmt.Errorf("agent with name %q already exists", name)
	}

	agentName := af.Agent.Name
	if agentName == "" {
		agentName = name
	}

	var overrides []spec.Override

	if req.GetModel() != "" && req.GetModel() != af.Agent.Model {
		overrides = append(overrides, spec.WithModel(req.GetModel()))
	}

	if req.GetRuntime() != "" && req.GetRuntime() != af.Agent.Runtime {
		overrides = append(overrides, spec.WithRuntime(req.GetRuntime()))
	}

	if extra := envsFromRequest(req.GetEnvs()); len(extra) > 0 {
		overrides = append(overrides, spec.WithExtraEnvs(extra))
	}

	mounts, err := validateMounts(req.GetMounts())
	if err != nil {
		return nil, err
	}

	if specMounts := mountsForSpec(mounts); len(specMounts) > 0 {
		overrides = append(overrides, spec.WithMounts(specMounts))
	}

	// digestResolver runs from inside the workspace materialiser, which
	// happens in pool.Add's goroutine well after this gRPC handler
	// returns. Capturing the request ctx would mean every call sees
	// "context canceled". Detach with a fresh, time-bounded context.
	digestResolver := func(r string) string {
		bgctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		return d.resolveLocalDigest(bgctx, r)
	}

	agentOpts := []system.AgentOption{
		system.WithModelResolver(d.providers.Resolve),
		system.WithImageRef(ref.String()),
		system.WithDigestResolver(digestResolver),
	}

	if len(mounts) > 0 {
		agentOpts = append(agentOpts, system.WithMounts(mounts))
	}

	// Mint the agent's JWT BEFORE pool.Add so the token can be threaded
	// into the spawn env as OTTERS_AGENT_TOKEN. Skip if the daemon
	// was built without a signing key (defensive — every code path
	// that constructs a Daemon goes through serve.go's
	// LoadOrCreateSigningKey now, but tests may not).
	var agentToken, agentJTI string
	if len(d.signingKey) > 0 {
		var tokErr error
		agentToken, agentJTI, tokErr = auth.IssueAgent(d.signingKey, id.String())
		if tokErr != nil {
			return nil, fmt.Errorf("issuing agent token: %w", tokErr)
		}
	}

	d.pool.Add(id, ref, agentOpts, AgentExtras{
		DaemonURL:  d.agentReachableURL(),
		AgentToken: agentToken,
	}, overrides...)

	// Provenance fields are populated lazily by hydrateProvenance the
	// first time List() inspects this agent — the workspace is what
	// writes agent.yaml, and that runs in pool.Add's goroutine.
	//
	// Initial status is "pulling": pool.Add kicks off Run() in a
	// goroutine, and the very first transition the executor emits is
	// StatusPulling. Persisting this here means a daemon crash between
	// SaveAgent and the supervisor's first status update lands the
	// row at "pulling" (accurate) instead of a misleading "created".
	envOverrides := envsFromRequest(req.GetEnvs())

	ma := &managedAgent{
		id:           id,
		name:         name,
		agentName:    agentName,
		model:        af.Agent.Model,
		tag:          stripLoopbackPrefixes(ref.String()),
		status:       statusPulling,
		createdAt:    time.Now(),
		mounts:       mounts,
		envOverrides: envOverrides,
		labels:       req.GetLabels(),
		token:        agentToken,
		tokenJTI:     agentJTI,
	}

	if req.GetModel() != "" {
		ma.model = req.GetModel()
	}

	d.agents[id.String()] = ma

	// Effective runtime: operator override wins, otherwise the
	// Agentfile's baked RUNTIME directive. Persisting the effective
	// value is what makes the override survive a daemon restart —
	// af.Agent.Runtime alone loses the operator's choice.
	effectiveRuntime := af.Agent.Runtime
	if req.GetRuntime() != "" {
		effectiveRuntime = req.GetRuntime()
	}

	if saveErr := d.state.SaveAgent(ctx, persistedAgent{
		ID:        id.String(),
		Name:      name,
		AgentName: agentName,
		Model:     ma.model,
		Runtime:   effectiveRuntime,
		Tag:       ma.tag,
		Status:    statusPulling,
		CreatedAt: ma.createdAt,
		Mounts:    mountsToPersisted(mounts),
		Labels:    ma.labels,
		Envs:      envsToPersisted(envOverrides),
		Token:     ma.token,
		TokenJTI:  ma.tokenJTI,
	}); saveErr != nil {
		d.logger.Warn("failed to persist agent", zap.Error(saveErr))
	}

	d.logger.Info("agent created",
		zap.String("id", id.String()),
		zap.String("name", name),
		zap.String("agent", agentName),
		zap.String("model", ma.model),
		zap.String("status", ma.status),
	)

	return &daemonv1.CreateAgentResponse{
		Id: id.String(), Name: name, Status: ma.status,
	}, nil
}

// hydrateProvenance lazily reads each agent's etc/agent.yaml on the
// first List() call after creation/restore and copies the workspace-
// written Provenance + per-tool Ref/Digest fields into managedAgent's
// in-memory cache. Best-effort: if the chroot or agent.yaml isn't
// present yet (workspace materialisation runs in pool.Add's
// goroutine and is async w.r.t. List), the cached fields stay empty
// and the next call retries.
func (d *Daemon) hydrateProvenance(ma *managedAgent) {
	if ma.imageDigest != "" || len(ma.tools) > 0 {
		return
	}

	chroot := filepath.Join(d.agentsDir, ma.id.String())

	rt, err := agentpkg.LoadRuntime(osfs.New(chroot))
	if err != nil {
		return
	}

	if rt.Provenance != nil {
		ma.imageDigest = rt.Provenance.ImageDigest
		ma.runtimeRef = rt.Provenance.RuntimeRef
		ma.runtimeDigest = rt.Provenance.RuntimeDigest
	}

	if len(rt.Tools) > 0 {
		ma.tools = make([]agentTool, 0, len(rt.Tools))
		for _, t := range rt.Tools {
			ma.tools = append(ma.tools, agentTool{
				Name:        t.Name,
				Ref:         t.Ref,
				Digest:      t.Digest,
				Description: t.Description,
			})
		}
	}
}

// List returns every agent the daemon knows about. labelSelector
// drops any agent that doesn't have all the requested key=value
// pairs (logical AND, missing keys never match). Filter is applied
// in-memory: there's no SQL query — d.agents IS the live state.
func (d *Daemon) List(labelSelector map[string]string) []*daemonv1.AgentInfo {
	infos := make([]*daemonv1.AgentInfo, 0, len(d.agents))

	for _, ma := range d.agents {
		if !labelsMatch(ma.labels, labelSelector) {
			continue
		}
		d.hydrateProvenance(ma)

		status := ma.status
		failureReason := ""

		var addr string
		if a, ok := d.pool.Get(ma.id); ok {
			switch agt := a.(type) {
			case *system.Agent:
				addr = agt.Addr()
			case *docker.Agent:
				addr = agt.Addr()
			}

			status = a.Status().String()
			failureReason = a.FailureReason().String()
		}

		tools := make([]*daemonv1.AgentTool, 0, len(ma.tools))
		for _, t := range ma.tools {
			tools = append(tools, &daemonv1.AgentTool{
				Name:        t.Name,
				Ref:         t.Ref,
				Digest:      t.Digest,
				Description: t.Description,
			})
		}

		infos = append(infos, &daemonv1.AgentInfo{
			Id: ma.id.String(), Name: ma.name, Model: ma.model,
			Status:        status,
			FailureReason: failureReason,
			CreatedAt:     ma.createdAt.Unix(),
			Addr:          addr,
			Image:         ma.tag,
			Mounts:        mountsToProto(ma.mounts),
			ImageDigest:   ma.imageDigest,
			RuntimeRef:    ma.runtimeRef,
			RuntimeDigest: ma.runtimeDigest,
			Tools:         tools,
			Labels:        ma.labels,
		})
	}

	return infos
}

// labelsMatch reports whether `have` contains every key=value in
// `want`. Empty `want` matches everything. Empty `have` only
// matches an empty `want`. Used by Daemon.List for the in-memory
// agent label-selector filter; the asyncjobs store applies an
// equivalent filter in SQL.
func labelsMatch(have, want map[string]string) bool {
	if len(want) == 0 {
		return true
	}
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

func (d *Daemon) Stop(ctx context.Context, ref string) error {
	ma, err := d.resolve(ref)
	if err != nil {
		return err
	}

	if stopErr := d.pool.Stop(ctx, ma.id); stopErr != nil {
		return stopErr
	}

	ma.status = statusStopped

	if updateErr := d.state.UpdateStatus(ctx, ma.id.String(), statusStopped); updateErr != nil {
		d.logger.Warn("failed to persist stop", zap.Error(updateErr))
	}

	d.logger.Info("agent stopped", zap.String("id", ma.id.String()), zap.String("name", ma.name))

	return nil
}

func (d *Daemon) Remove(ctx context.Context, ref string) error {
	ma, err := d.resolve(ref)
	if err != nil {
		return err
	}

	if rmPoolErr := d.pool.Remove(ctx, ma.id); rmPoolErr != nil {
		d.logger.Warn("pool remove failed", zap.Error(rmPoolErr))
	}

	delete(d.agents, ma.id.String())

	// Drop async-jobs for the agent first — belt-and-braces for hosts
	// running without --sqlite-foreign-key (the FK cascade in
	// migrateState handles the rest, but only when enforcement is on).
	if jobsRemoved, jobsErr := asyncjobs.NewStore(d.state.db).
		DeleteByAgent(ctx, ma.id.String()); jobsErr != nil {
		d.logger.Warn("async-jobs remove failed", zap.Error(jobsErr))
	} else if jobsRemoved > 0 {
		d.logger.Info("async-jobs cascaded with agent",
			zap.Int64("rows", jobsRemoved),
			zap.String("agent", ma.id.String()))
	}

	// Revoke the agent's JWT before deleting the agent row — the FK
	// cascade only removes the agents row (and async_jobs), not the
	// jti from the token's claims. Without this, a leaked token
	// would remain valid until exp (10y) and let the holder dial
	// the daemon as the deleted agent. Best-effort: a failure here
	// just means the token still validates until exp; the agent
	// is gone either way so it can't reach anything anyway.
	if ma.tokenJTI != "" {
		if revErr := d.state.RevokeToken(ctx, ma.tokenJTI, "agent removed"); revErr != nil {
			d.logger.Warn("failed to revoke agent token", zap.Error(revErr))
		}
	}

	if rmErr := d.state.RemoveAgent(ctx, ma.id.String()); rmErr != nil {
		d.logger.Warn("failed to persist removal", zap.Error(rmErr))
	}

	d.logger.Info("agent removed", zap.String("id", ma.id.String()), zap.String("name", ma.name))

	return nil
}

func (d *Daemon) ChatWithAgent(ctx context.Context, ref, sessionID, prompt string) (string, error) {
	ma, err := d.resolve(ref)
	if err != nil {
		return "", err
	}

	a, ok := d.pool.Get(ma.id)
	if !ok || a == nil {
		return "", fmt.Errorf("agent %q is not running", ma.name)
	}

	prompter, ok := a.(agentpkg.Prompter)
	if !ok {
		return "", fmt.Errorf("agent %q does not support chat", ma.name)
	}

	var buf strings.Builder

	req := agentpkg.PromptRequest{SessionID: sessionID, Prompt: prompt}
	if promptErr := prompter.Prompt(ctx, req, &buf); promptErr != nil {
		return "", promptErr
	}

	return buf.String(), nil
}

// PromptObjectWithAgent runs a one-shot structured-output query
// against the agent identified by ref. Stateless — no session
// memory, no tool loop. Returns the object as raw JSON bytes, ready
// to be placed on the wire.
func (d *Daemon) PromptObjectWithAgent(
	ctx context.Context, ref, prompt string, schema []byte, schemaName, schemaDesc string,
) ([]byte, error) {
	ma, err := d.resolve(ref)
	if err != nil {
		return nil, err
	}

	a, ok := d.pool.Get(ma.id)
	if !ok || a == nil {
		return nil, fmt.Errorf("agent %q is not running", ma.name)
	}

	prompter, ok := a.(agentpkg.ObjectPrompter)
	if !ok {
		return nil, fmt.Errorf("agent %q does not support structured output", ma.name)
	}

	return prompter.PromptObject(ctx, agentpkg.ObjectPromptRequest{
		Prompt:            prompt,
		Schema:            schema,
		SchemaName:        schemaName,
		SchemaDescription: schemaDesc,
	})
}

// ListSessionMessages returns historical messages for (ref, sessionID)
// by asking the running agent's SessionReader. Used by the CLI to
// preload prompt history when a chat session opens.
func (d *Daemon) ListSessionMessages(
	ctx context.Context, ref, sessionID string, limit int,
) ([]agentpkg.SessionMessage, error) {
	ma, err := d.resolve(ref)
	if err != nil {
		return nil, err
	}

	a, ok := d.pool.Get(ma.id)
	if !ok || a == nil {
		return nil, fmt.Errorf("agent %q is not running", ma.name)
	}

	reader, ok := a.(agentpkg.SessionReader)
	if !ok {
		return nil, fmt.Errorf("agent %q does not expose session history", ma.name)
	}

	return reader.ListSessionMessages(ctx, sessionID, limit)
}

// ListSessions enumerates the live agent's session log. Returns an
// empty slice (not an error) for agents that don't implement
// SessionLister, so the dashboard can render "no history yet" on
// older agents without surfacing an error toast.
func (d *Daemon) ListSessions(ctx context.Context, ref string) ([]agentpkg.SessionInfo, error) {
	ma, err := d.resolve(ref)
	if err != nil {
		return nil, err
	}

	a, ok := d.pool.Get(ma.id)
	if !ok || a == nil {
		return nil, fmt.Errorf("agent %q is not running", ma.name)
	}

	lister, ok := a.(agentpkg.SessionLister)
	if !ok {
		return []agentpkg.SessionInfo{}, nil
	}

	return lister.ListSessions(ctx)
}

// DeleteSession removes a single session from the live agent's session
// store. Idempotent on the runtime side.
func (d *Daemon) DeleteSession(ctx context.Context, ref, sessionID string) error {
	ma, err := d.resolve(ref)
	if err != nil {
		return err
	}

	a, ok := d.pool.Get(ma.id)
	if !ok || a == nil {
		return fmt.Errorf("agent %q is not running", ma.name)
	}

	deleter, ok := a.(agentpkg.SessionDeleter)
	if !ok {
		return fmt.Errorf("agent %q does not support session deletion", ma.name)
	}

	return deleter.DeleteSession(ctx, sessionID)
}

// ChatStreamWithAgent invokes the agent's StreamPrompter and forwards every
// event to cb as it arrives. Callers (the gRPC stream handler) translate cb
// invocations into wire events.
func (d *Daemon) ChatStreamWithAgent(
	ctx context.Context, ref, sessionID, prompt string, regenerate bool, cb func(agentpkg.PromptEvent),
) error {
	ma, err := d.resolve(ref)
	if err != nil {
		return err
	}

	a, ok := d.pool.Get(ma.id)
	if !ok || a == nil {
		return fmt.Errorf("agent %q is not running", ma.name)
	}

	streamer, ok := a.(agentpkg.StreamPrompter)
	if !ok {
		return fmt.Errorf("agent %q does not support streaming chat", ma.name)
	}

	return streamer.PromptStream(ctx, agentpkg.PromptRequest{
		SessionID:  sessionID,
		Prompt:     prompt,
		Regenerate: regenerate,
	}, cb)
}

func (d *Daemon) nameExists(name string) bool {
	for _, ma := range d.agents {
		if ma.name == name {
			return true
		}
	}

	return false
}

// resolveLocalDigest looks up the embedded registry's manifest for ref
// (host:port stripped) and returns its content-addressed digest. Empty
// string on any failure — callers treat the digest as best-effort
// metadata, not load-bearing for correctness.
func (d *Daemon) resolveLocalDigest(ctx context.Context, ref string) string {
	if ref == "" || d.registry == nil {
		return ""
	}

	addr := d.registry.Addr()
	repo, tag := splitRef(stripLoopbackPrefixes(ref))

	if repo == "" || tag == "" {
		return ""
	}

	info, err := fetchManifestInfo(ctx, addr, repo, tag)
	if err != nil {
		return ""
	}

	return info.digest
}

func (d *Daemon) resolve(ref string) (*managedAgent, error) {
	if ma, ok := d.agents[ref]; ok {
		return ma, nil
	}

	for _, ma := range d.agents {
		if ma.name == ref {
			return ma, nil
		}
	}

	for id, ma := range d.agents {
		if strings.HasPrefix(id, ref) {
			return ma, nil
		}
	}

	return nil, fmt.Errorf("agent %q not found", ref)
}

// refFor resolves a user-supplied tag to the right shape for the
// active executor backend. The system executor stores everything in
// the embedded oras registry, which requires the embedded address
// as a name prefix; the docker executor uses Docker's image store
// where refs stay as the user wrote them.
func (d *Daemon) refFor(tag string) spec.Reference {
	if d.executor == executorDocker {
		return spec.ParseReference(tag)
	}

	return d.localRef(tag)
}

// openReadTarget returns an oras.ReadOnlyTarget that resolves ref
// against the active executor's storage backend. The docker
// executor uses a docker.Store (which hydrates via cli.ImageSave);
// the system executor uses the embedded registry's HTTP target.
//
// Used by CreateAgent's metadata-load path; the pool's storeFor
// closure does the same thing on the per-agent build/run path.
// They're separate calls because CreateAgent runs synchronously
// against the daemon while storeFor lazily wraps the per-ref
// Provider hand-off.
func (d *Daemon) openReadTarget(ref spec.Reference) (oras.ReadOnlyTarget, error) {
	if d.executor == executorDocker {
		cli, err := docker.NewClient()
		if err != nil {
			return nil, fmt.Errorf("docker store: open client: %w", err)
		}

		return docker.NewStore(cli), nil
	}

	t, err := newRegistryTarget(ref)
	if err != nil {
		return nil, err
	}

	return t, nil
}

// openBuildStore returns the oras.Target every build pipeline
// pushes blobs into. The docker backend writes through to the
// Docker daemon's image store via OCI image layout + ImageLoad;
// the system backend uses an in-memory store that pushImage later
// copies into the embedded HTTP registry.
//
// Returned closure: nil-safe — falls back to orasmem when the
// docker client can't be opened so callers don't have to special-
// case startup ordering. The error path logs once and the caller
// gets a working in-memory store.
func (d *Daemon) openBuildStore() oras.Target {
	if d.executor == executorDocker {
		cli, err := docker.NewClient()
		if err != nil {
			d.logger.Error("docker store: open client", zap.Error(err))
			return orasmem.New()
		}

		return docker.NewStore(cli)
	}

	return orasmem.New()
}

// upsertImagesFromTags re-fetches each tag from the executor
// registry and upserts a full row into the daemon's images cache.
// Used by Pull / Save / Push to keep the DB in sync with the
// executor's actual state after a successful operation. Failures
// are logged but not surfaced — the primary operation already
// succeeded.
//
// kindHint is the artifactType the caller already knows (build
// paths supply it). When non-empty it wins over whatever
// ManifestKind returns; otherwise we ask the registry.
func (d *Daemon) upsertImagesFromTags(ctx context.Context, tags []string, kindHint string) {
	if len(tags) == 0 {
		return
	}

	reg := d.pool.provider.Registry()

	// Carry-forward source for kinds the registry can't surface
	// itself (docker's ManifestKind is always empty). Loaded once;
	// nil on lookup failure means "no carry-forward available".
	var existing []PersistedImage

	for _, tag := range tags {
		info, err := reg.Inspect(ctx, tag)
		if err != nil {
			d.logger.Debug("inspect for image cache",
				zap.String("ref", tag), zap.Error(err))

			continue
		}

		kind := kindHint

		if kind == "" {
			if k, kErr := reg.ManifestKind(ctx, tag); kErr == nil {
				kind = k
			}
		}

		// On docker, ManifestKind can't see the manifest's
		// artifactType — re-pulls / re-pushes would otherwise
		// blank the kind and drop the row from filtered listings.
		// Preserve whatever the cache last knew for this ref or
		// digest.
		if kind == "" {
			if existing == nil {
				if rows, listErr := d.state.ListImages(ctx); listErr == nil {
					existing = rows
				}
			}

			for _, e := range existing {
				if (e.Ref == tag || e.Digest == info.Digest) && e.ArtifactType != "" {
					kind = e.ArtifactType

					break
				}
			}
		}

		// Last resort for first-time pulls on docker: ask the
		// upstream registry directly via ORAS. Bypasses the docker
		// daemon's manifest-blob restriction, so bins / agents
		// pulled via the dashboard's "Pull from URL" button land
		// classified instead of as "unknown".
		if kind == "" {
			if k, kErr := d.fetchRemoteManifestKind(ctx, tag); kErr == nil {
				kind = k
			} else {
				d.logger.Debug("fetch remote manifest kind",
					zap.String("ref", tag), zap.Error(kErr))
			}
		}

		// Docker's ImageInspect returns Created=null for OCI
		// artifacts whose config blob has a custom mediatype (our
		// agent images), so info.CreatedUnix lands as 0 and the UI
		// renders 1970. Fall back to "now" — the moment we ingested
		// the artifact is the closest stable approximation we have.
		createdUnix := info.CreatedUnix
		if createdUnix == 0 {
			createdUnix = time.Now().Unix()
		}

		// Cache the describe-time fields (config blob + label set +
		// layer summary) so DescribeImage doesn't need an ImageSave
		// per call. Best-effort: each helper logs + returns empties
		// on failure so the row still lands with the cheap fields
		// from Inspect.
		configJSON, layersJSON := d.cacheableDescribeBlobs(ctx, tag)
		labelsJSON := encodeLabels(info.Annotations)

		if upsertErr := d.state.UpsertImage(ctx, PersistedImage{
			Ref:          tag,
			Digest:       info.Digest,
			ArtifactType: kind,
			Size:         info.Size,
			CreatedUnix:  createdUnix,
			Description:  info.Description,
			Source:       info.Source,
			ConfigJSON:   configJSON,
			LabelsJSON:   labelsJSON,
			LayersJSON:   layersJSON,
		}); upsertErr != nil {
			d.logger.Warn("upsert image cache",
				zap.String("ref", tag), zap.Error(upsertErr))
		}
	}
}

// cacheableDescribeBlobs fetches the manifest's config blob + a
// formatted layer summary for ref. Used at ingest time
// (upsertImagesFromTags) to pre-warm the describe cache so the
// dashboard's image-detail page doesn't trigger an ImageSave round
// trip per visit. Returns empty strings on any failure — the row
// still lands with the cheap fields from Inspect, and DescribeImage
// falls back to a live fetch if it needs richer data.
//
// Both return values are strings (config = raw JSON of the agent
// spec; layers = JSON-encoded []string of "title (mediatype, N bytes)"
// summaries) to fit the persisted-image schema directly. Empty
// means "couldn't fetch / not applicable" rather than "empty data".
func (d *Daemon) cacheableDescribeBlobs(ctx context.Context, ref string) (string, string) {
	if d.executor != executorDocker || d.storeFor == nil {
		// System executor's describeImageEmbedded fetches on demand
		// and is cheap (local HTTP); caching is a nice-to-have, not
		// load-bearing. Skip the pre-warm there.
		return "", ""
	}

	store := d.storeFor(spec.ParseReference(ref))

	manifestDesc, err := store.Resolve(ctx, ref)
	if err != nil {
		d.logger.Debug("describe cache: resolve",
			zap.String("ref", ref), zap.Error(err))

		return "", ""
	}

	manifestRC, err := store.Fetch(ctx, manifestDesc)
	if err != nil {
		d.logger.Debug("describe cache: fetch manifest",
			zap.String("ref", ref), zap.Error(err))

		return "", ""
	}

	manifestData, err := io.ReadAll(manifestRC)
	_ = manifestRC.Close()
	if err != nil {
		return "", ""
	}

	var manifest ocispec.Manifest
	if err = json.Unmarshal(manifestData, &manifest); err != nil {
		return "", ""
	}

	var configJSON string
	if manifest.Config.Digest != "" {
		if cfgRC, cfgErr := store.Fetch(ctx, manifest.Config); cfgErr == nil {
			if cfgData, readErr := io.ReadAll(cfgRC); readErr == nil {
				configJSON = string(cfgData)
			}
			_ = cfgRC.Close()
		}
	}

	layers := make([]string, 0, len(manifest.Layers))
	for _, l := range manifest.Layers {
		title := l.Annotations["org.opencontainers.image.title"]
		if title == "" {
			title = l.Digest.String()[:16]
		}

		layers = append(layers, fmt.Sprintf("%s (%s, %d bytes)", title, l.MediaType, l.Size))
	}

	layersJSON, _ := json.Marshal(layers)

	return configJSON, string(layersJSON)
}

// encodeLabels serialises a label map for the cache column. Stable
// JSON object so consumers (the DescribeImage RPC) can decode
// without surprises; nil / empty input maps to "{}" rather than
// "null" so the DB column never holds a JSON null.
func encodeLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}

	data, err := json.Marshal(labels)
	if err != nil {
		return "{}"
	}

	return string(data)
}

// commitBuilt makes the freshly built manifest visible at ref. For
// the docker backend this calls store.Tag — the underlying
// docker.Store flushes the staged blobs into Docker via ImageLoad
// the first time a ref is tagged, and idempotently re-tags on
// subsequent calls. For the system backend it copies from the
// in-memory build store into the embedded HTTP registry.
//
// srcRef is what the executor's store keys its blobs under (a
// digest for docker, "latest" for the system executor's bin
// pipeline). digest is the manifest's content-addressed digest
// that gets indexed in the images cache; artifactType is the openotters
// kind ("application/vnd.openotters.{agent,bin}.v1") the build
// pipeline produced.
func (d *Daemon) commitBuilt(
	ctx context.Context, store oras.Target, srcRef string, ref spec.Reference,
	manifestDigest, artifactType string,
) error {
	if err := d.commitBuiltStorage(ctx, store, srcRef, ref); err != nil {
		return err
	}

	// Refresh the daemon's image cache for this ref so ListImages
	// surfaces the just-built artifact without waiting for a
	// manual RefreshImages call. Failures are logged best-effort —
	// the storage write already succeeded.
	d.upsertImagesFromTags(ctx, []string{ref.String()}, artifactType)

	_ = manifestDigest // currently embedded in the upsert via Inspect

	return nil
}

// commitBuiltStorage copies the build store's manifest into the
// executor-native registry. Split from commitBuilt so the
// per-executor branching stays a single decision point.
func (d *Daemon) commitBuiltStorage(
	ctx context.Context, store oras.Target, srcRef string, ref spec.Reference,
) error {
	if d.executor == executorDocker {
		dockerStore, ok := store.(*docker.Store)
		if !ok {
			return fmt.Errorf("commit: expected *docker.Store, got %T", store)
		}

		dgst, err := digest.Parse(srcRef)
		if err != nil {
			return fmt.Errorf("commit: parse digest %s: %w", srcRef, err)
		}

		desc := ocispec.Descriptor{Digest: dgst}

		return dockerStore.Tag(ctx, desc, ref.String())
	}

	// System path: the in-memory build store needs to be copied
	// into the embedded HTTP registry under the embedded-prefixed
	// ref. pushImage does that via oras.Copy + a remote.Repository.
	memStore, ok := store.(*orasmem.Store)
	if !ok {
		return fmt.Errorf("commit: expected *orasmem.Store, got %T", store)
	}

	return d.pushImage(ctx, memStore, srcRef, ref)
}

// localRef rewrites a user-provided tag so it points at the daemon's
// embedded registry. Any ref not already prefixed with the embedded
// address gets that address prepended verbatim — bare `name:tag`
// lands at `<embeddedAddr>/name:tag`, and a remote ref like
// `ghcr.io/openotters/agents/base:latest` lands at
// `<embeddedAddr>/ghcr.io/openotters/agents/base:latest`, preserving
// the source path as the local repo name (the layout
// resolveStoredTag was designed for). Refs already targeting the
// embedded registry are returned untouched, which is what keeps
// `image build -t <embeddedAddr>/agents/foo:v1` from being
// double-prefixed.
func (d *Daemon) localRef(tag string) spec.Reference {
	if d.registry == nil {
		// Docker executor path doesn't run an embedded registry —
		// refFor short-circuits there, but localRef may still be
		// called by code paths that aren't yet executor-aware
		// (e.g. agent restore). Fall back to the user's tag
		// verbatim, which lands in Docker's image store as-is.
		return spec.ParseReference(tag)
	}

	return qualifyForEmbeddedRegistry(tag, d.registry.Addr())
}

// qualifyForEmbeddedRegistry is the pure side of localRef, split out
// so it can be tested without standing up an embedded registry.
func qualifyForEmbeddedRegistry(tag, embeddedAddr string) spec.Reference {
	ref := spec.ParseReference(tag)
	if strings.HasPrefix(ref.Name, embeddedAddr+"/") {
		return ref
	}

	return spec.Reference{
		Name: embeddedAddr + "/" + ref.Name,
		Tag:  ref.Tag,
	}
}

// fetchRemoteManifestKind talks directly to the upstream registry via
// ORAS, bypassing the executor backend, and returns the manifest's
// `artifactType` field (empty when absent). The docker executor can't
// surface this for OCI artifacts whose config blob has a custom
// mediatype, but the registry itself always knows — the manifest is
// just JSON. Used to classify first-time docker pulls of bins and
// agents from a remote ref like ghcr.io/...
func (d *Daemon) fetchRemoteManifestKind(ctx context.Context, ref string) (string, error) {
	parsed := spec.ParseReference(ref)
	if parsed.Name == "" {
		return "", fmt.Errorf("ref %s has no registry portion", ref)
	}

	repo, err := agentoci.NewRemoteRepository(parsed)
	if err != nil {
		return "", fmt.Errorf("repo for %s: %w", ref, err)
	}

	tag := parsed.Tag
	if tag == "" {
		tag = defaultTag
	}

	desc, err := repo.Resolve(ctx, tag)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", ref, err)
	}

	rc, err := repo.Fetch(ctx, desc)
	if err != nil {
		return "", fmt.Errorf("fetch manifest %s: %w", ref, err)
	}
	defer func() { _ = rc.Close() }()

	body, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("read manifest %s: %w", ref, err)
	}

	var manifest struct {
		ArtifactType string `json:"artifactType"`
	}
	if uErr := json.Unmarshal(body, &manifest); uErr != nil {
		return "", fmt.Errorf("unmarshal manifest %s: %w", ref, uErr)
	}

	return manifest.ArtifactType, nil
}

// pushImage copies srcRef from store into the embedded registry at
// ref. Used by the system executor's commitBuilt — the docker
// executor writes through docker.Store directly and never enters
// this path.
func (d *Daemon) pushImage(
	ctx context.Context, store *orasmem.Store, srcRef string, ref spec.Reference,
) error {
	repo, err := agentoci.NewRemoteRepository(ref)
	if err != nil {
		return fmt.Errorf("creating repository for %s: %w", ref, err)
	}

	tag := ref.Tag
	if tag == "" {
		tag = defaultTag
	}

	if _, err = oras.Copy(ctx, store, srcRef, repo, tag, copyOptions()); err != nil {
		return err
	}

	return nil
}

func importArtifact(ctx context.Context, data []byte) (*orasmem.Store, string, error) {
	return export.Import(ctx, data)
}
