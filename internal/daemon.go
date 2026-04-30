package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-billy/v6/osfs"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"oras.land/oras-go/v2"
	orasmem "oras.land/oras-go/v2/content/memory"

	agentpkg "github.com/openotters/agentfile/agent"
	"github.com/openotters/agentfile/agent/system"
	agentbuild "github.com/openotters/agentfile/build"
	"github.com/openotters/agentfile/export"
	agentoci "github.com/openotters/agentfile/oci"
	"github.com/openotters/agentfile/spec"
	afstore "github.com/openotters/agentfile/store"
	"github.com/openotters/bin/pkg/bin"
	daemonv1 "github.com/openotters/openotters/api/v1"
)

const (
	statusCreated    = "created"
	statusStopped    = "stopped"
	statusPending    = "pending"
	statusRunning    = "running"
	statusInitError  = "init_error"
	statusModelError = "model_error"
	defaultTag       = "latest"
)

type managedAgent struct {
	id        uuid.UUID
	name      string
	agentName string
	model     string
	tag       string
	status    string
	createdAt time.Time
	mounts    []system.Mount

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
// system.Mount struct the provider consumes. Inverse of the
// conversion inside CreateAgent.
func mountsFromPersisted(pms []persistedMount) []system.Mount {
	if len(pms) == 0 {
		return nil
	}

	out := make([]system.Mount, 0, len(pms))
	for _, pm := range pms {
		out = append(out, system.Mount{
			Host:        pm.Host,
			Target:      pm.Target,
			Description: pm.Description,
		})
	}

	return out
}

// mountsToPersisted is the write-side counterpart used when
// persisting a freshly-created agent's mounts to the state store.
func mountsToPersisted(ms []system.Mount) []persistedMount {
	if len(ms) == 0 {
		return nil
	}

	out := make([]persistedMount, 0, len(ms))
	for _, m := range ms {
		out = append(out, persistedMount{
			Host:        m.Host,
			Target:      m.Target,
			Description: m.Description,
		})
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
// the internal system.Mount form, rejecting anything that would leave
// the daemon in a bad state: relative/empty paths, missing host
// files, reserved target prefixes, duplicate targets. The client is
// expected to have resolved `~`/`$PWD`/relative host paths already.
func validateMounts(in []*daemonv1.Mount) ([]system.Mount, error) {
	if len(in) == 0 {
		return nil, nil
	}

	out := make([]system.Mount, 0, len(in))
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

		out = append(out, system.Mount{
			Host:        host,
			Target:      target,
			Description: m.GetDescription(),
		})
	}

	return out, nil
}

// mountsToProto converts system.Mount slices to the protobuf form
// returned by ListAgents so the CLI `otters agent inspect` / `ps -v`
// can surface them.
func mountsToProto(ms []system.Mount) []*daemonv1.Mount {
	if len(ms) == 0 {
		return nil
	}

	out := make([]*daemonv1.Mount, 0, len(ms))
	for _, m := range ms {
		out = append(out, &daemonv1.Mount{
			Host:        m.Host,
			Target:      m.Target,
			Description: m.Description,
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
	logDir    string
	agentsDir string
	dataDir   string
	runtime   string
	socket    string
	version   string
	commit    string
	buildDate string
	catwalk   *catwalkCatalogue

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
}

// DaemonOption configures the Daemon at construction time.
type DaemonOption func(*daemonConfig)

type daemonConfig struct {
	localRuntime    string
	socket          string
	version         string
	commit          string
	buildDate       string
	maxConcurrent   int
	backoffBase     time.Duration
	backoffCap      time.Duration
	shutdownTimeout time.Duration
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

	providerOpts := []system.ProviderOption{
		system.WithLogDir(logDir),
	}

	if cfg.localRuntime != "" {
		providerOpts = append(providerOpts, system.WithLocalRuntime(cfg.localRuntime))
	}

	if reg != nil {
		providerOpts = append(providerOpts, system.WithPuller(newCachingBinPuller(reg.Addr())))
	}

	// Each agent gets a target bound to its own ref, resolving against the
	// embedded registry directly — no in-memory staging. On construction
	// failure we hand back a stub that returns the underlying error on
	// every call so the agent surfaces it cleanly instead of nil-derefing
	// in afstore.Load.
	storeFor := func(ref spec.Reference) oras.ReadOnlyTarget {
		t, err := newRegistryTarget(ref)
		if err != nil {
			logger.Error("registry target", zap.String("ref", ref.String()), zap.Error(err))

			return erroringTarget{err: err}
		}

		return t
	}

	provider := system.NewProvider(root, storeFor, providerOpts...)

	daemonLogger := logger.Named("daemon")

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

	return &Daemon{
		pool:            NewPool(provider, poolOpts...),
		providers:       providers,
		registry:        reg,
		state:           state,
		logger:          daemonLogger,
		logDir:          logDir,
		agentsDir:       agentsDir,
		dataDir:         dataDir,
		runtime:         cfg.localRuntime,
		socket:          cfg.socket,
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
}

// Info returns a snapshot of the daemon's runtime coordinates for
// display by `otters info`. Cheap — everything is in-memory.
func (d *Daemon) Info() DaemonInfo {
	running := 0

	for _, ma := range d.agents {
		if a, ok := d.pool.Get(ma.id); ok && a.Status().String() == statusRunning {
			running++
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

	ma.status = statusRunning

	if updateErr := d.state.UpdateStatus(ctx, ma.id.String(), statusRunning); updateErr != nil {
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

		mounts := mountsFromPersisted(pa.Mounts)

		d.agents[pa.ID] = &managedAgent{
			id:        id,
			name:      pa.Name,
			agentName: pa.AgentName,
			model:     pa.Model,
			tag:       pa.Tag,
			status:    pa.Status,
			createdAt: pa.CreatedAt,
			mounts:    mounts,
		}

		agentOpts := []system.AgentOption{
			system.WithModelResolver(d.providers.Resolve),
		}

		if len(mounts) > 0 {
			agentOpts = append(agentOpts, system.WithMounts(mounts))
		}

		if pa.Status != statusStopped {
			d.pool.Add(id, ref, agentOpts, overrides...)

			d.agents[pa.ID].status = statusRunning
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

	store := orasmem.New()

	built, err := agentbuild.FromFile(ctx, abs, store)
	if err != nil {
		return nil, fmt.Errorf("building %s: %w", abs, err)
	}

	tags := req.GetTags()
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
		ref := d.localRef(tag)
		if _, pushErr := d.pushImage(ctx, store, srcRef, ref); pushErr != nil {
			return nil, fmt.Errorf("pushing %s: %w", tag, pushErr)
		}

		pushed = append(pushed, tag)
	}

	d.logger.Info("agent built",
		zap.String("path", abs),
		zap.String("digest", built.Digest.String()),
		zap.Strings("tags", pushed),
	)

	return &daemonv1.BuildAgentResponse{
		Digest: built.Digest.String(),
		Tags:   pushed,
		Ref:    built.Reference.String(),
	}, nil
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

	store := orasmem.New()

	dig, err := bin.BuildIndex(ctx, bin.BuildOptions{
		Name:        name,
		BinPath:     platforms[0].BinPath,
		Description: req.GetDescription(),
		Usage:       req.GetUsage(),
	}, platforms, store)
	if err != nil {
		return nil, fmt.Errorf("building: %w", err)
	}

	tags := req.GetTags()
	if len(tags) == 0 {
		tags = []string{name + ":" + spec.DefaultTag}
	}

	// bin.BuildIndex tags the index as "latest" in the store.
	pushed := make([]string, 0, len(tags))
	for _, tag := range tags {
		ref := d.localRef(tag)
		if _, pushErr := d.pushImage(ctx, store, "latest", ref); pushErr != nil {
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

		if _, pushErr := d.pushImage(ctx, store, digest, ref); pushErr != nil {
			return nil, fmt.Errorf("saving %s to local registry: %w", tag, pushErr)
		}
	}

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

	store, err := d.pullImage(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("pulling %s: %w", ref, err)
	}

	// Local ref preserves the remote path verbatim under the embedded
	// registry. Additional `tags` on the request are treated as full
	// paths to mirror under as well (same shape as the remote ref).
	tag := ref.Tag
	if tag == "" {
		tag = defaultTag
	}

	tags := req.GetTags()
	if len(tags) == 0 {
		tags = []string{ref.Name + ":" + tag}
	}

	var digest string

	for _, t := range tags {
		localRef := d.localRef(t)

		dig, pushErr := d.pushImage(ctx, store, ref.String(), localRef)
		if pushErr != nil {
			return nil, fmt.Errorf("saving %s to local registry: %w", t, pushErr)
		}

		digest = dig
	}

	d.logger.Info("image pulled", zap.String("ref", req.GetRef()), zap.Strings("tags", tags))

	return &daemonv1.PullResponse{Digest: digest, Tags: tags}, nil
}

func (d *Daemon) Push(
	ctx context.Context, req *daemonv1.PushRequest,
) (*daemonv1.PushResponse, error) {
	ref := spec.ParseReference(req.GetRef())

	// Local and remote refs share the same repo path + tag. The local
	// copy lives at <embeddedAddr>/<Name>:<Tag>; pushing copies it
	// verbatim to <Name>:<Tag> on the remote registry. localRef qualifies
	// against the embedded registry only when ref isn't already qualified
	// — otherwise we'd double-prefix and 404.
	localRef := d.localRef(req.GetRef())

	d.logger.Info("pulling from local registry", zap.String("local", localRef.String()))

	store, err := d.pullImage(ctx, localRef)
	if err != nil {
		return nil, fmt.Errorf("pulling from local registry: %w", err)
	}

	d.logger.Info("pushing to remote", zap.String("ref", ref.String()))

	digest, err := d.pushImage(ctx, store, localRef.String(), ref)
	if err != nil {
		return nil, fmt.Errorf("pushing to %s: %w", ref, err)
	}

	return &daemonv1.PushResponse{Digest: digest, Ref: req.GetRef()}, nil
}

//nolint:funlen // sequential agent-creation flow reads more clearly straight-through
func (d *Daemon) CreateAgent(
	ctx context.Context,
	req *daemonv1.CreateAgentRequest,
) (*daemonv1.CreateAgentResponse, error) {
	// localRef qualifies the user's ref against the embedded registry
	// only when it isn't already qualified. Replaces the old
	// "no slash → unqualified" heuristic, which mis-classified refs
	// like `agents/foo:v1` (slash present, but still bare).
	ref := d.localRef(req.GetRef())

	// Load metadata straight from the embedded registry — no staging.
	target, err := newRegistryTarget(ref)
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

	mounts, err := validateMounts(req.GetMounts())
	if err != nil {
		return nil, err
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

	d.pool.Add(id, ref, agentOpts, overrides...)

	// Provenance fields are populated lazily by hydrateProvenance the
	// first time List() inspects this agent — the workspace is what
	// writes agent.yaml, and that runs in pool.Add's goroutine.
	ma := &managedAgent{
		id:        id,
		name:      name,
		agentName: agentName,
		model:     af.Agent.Model,
		tag:       stripLoopbackPrefixes(ref.String()),
		status:    statusCreated,
		createdAt: time.Now(),
		mounts:    mounts,
	}

	if req.GetModel() != "" {
		ma.model = req.GetModel()
	}

	d.agents[id.String()] = ma

	if saveErr := d.state.SaveAgent(ctx, persistedAgent{
		ID:        id.String(),
		Name:      name,
		AgentName: agentName,
		Model:     ma.model,
		Runtime:   af.Agent.Runtime,
		Tag:       ma.tag,
		Status:    statusCreated,
		CreatedAt: ma.createdAt,
		Mounts:    mountsToPersisted(mounts),
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

func (d *Daemon) List() []*daemonv1.AgentInfo {
	infos := make([]*daemonv1.AgentInfo, 0, len(d.agents))

	for _, ma := range d.agents {
		d.hydrateProvenance(ma)

		status := ma.status

		var addr string
		if a, ok := d.pool.Get(ma.id); ok {
			if sa, isSystem := a.(*system.Agent); isSystem {
				addr = sa.Addr()
			}

			status = a.Status().String()
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
			CreatedAt:     ma.createdAt.Unix(),
			Addr:          addr,
			Image:         ma.tag,
			Mounts:        mountsToProto(ma.mounts),
			ImageDigest:   ma.imageDigest,
			RuntimeRef:    ma.runtimeRef,
			RuntimeDigest: ma.runtimeDigest,
			Tools:         tools,
		})
	}

	return infos
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

// ChatStreamWithAgent invokes the agent's StreamPrompter and forwards every
// event to cb as it arrives. Callers (the gRPC stream handler) translate cb
// invocations into wire events.
func (d *Daemon) ChatStreamWithAgent(
	ctx context.Context, ref, sessionID, prompt string, cb func(agentpkg.PromptEvent),
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

	return streamer.PromptStream(ctx, agentpkg.PromptRequest{SessionID: sessionID, Prompt: prompt}, cb)
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

func (d *Daemon) pullImage(ctx context.Context, ref spec.Reference) (*orasmem.Store, error) {
	repo, err := agentoci.NewRemoteRepository(ref)
	if err != nil {
		return nil, fmt.Errorf("creating repository for %s: %w", ref, err)
	}

	store := orasmem.New()

	tag := ref.Tag
	if tag == "" {
		tag = defaultTag
	}

	// Tag in the store with the full reference so it's resolvable by spec.Reference.String().
	dstTag := ref.String()

	_, err = oras.Copy(ctx, repo, tag, store, dstTag, copyOptions())
	if err != nil {
		return nil, fmt.Errorf("copying %s: %w", ref, err)
	}

	return store, nil
}

func (d *Daemon) pushImage(
	ctx context.Context, store *orasmem.Store, srcRef string, ref spec.Reference,
) (string, error) {
	repo, err := agentoci.NewRemoteRepository(ref)
	if err != nil {
		return "", fmt.Errorf("creating repository for %s: %w", ref, err)
	}

	tag := ref.Tag
	if tag == "" {
		tag = defaultTag
	}

	desc, err := oras.Copy(ctx, store, srcRef, repo, tag, copyOptions())
	if err != nil {
		return "", err
	}

	return desc.Digest.String(), nil
}

func importArtifact(ctx context.Context, data []byte) (*orasmem.Store, string, error) {
	return export.Import(ctx, data)
}
