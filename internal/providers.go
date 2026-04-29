package internal

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

type ProviderConfig struct {
	Name    string   `yaml:"name"`
	APIKey  string   `yaml:"api-key"`
	APIBase string   `yaml:"api-base,omitempty"`
	Models  []string `yaml:"models,omitempty"`
}

type ProvidersFile struct {
	Providers []ProviderConfig `yaml:"providers"`
}

// ProviderRegistry serves provider configuration to the daemon. Reads
// are lazy: every public method calls snapshot(), which restats the
// underlying file and reloads when the mtime has advanced. Editing
// ~/.otters/providers.yaml takes effect on the next call without a
// daemon restart.
type ProviderRegistry struct {
	path     string
	logger   *zap.Logger
	readFile func(string) ([]byte, error) // test seam; defaults to os.ReadFile

	mu    sync.RWMutex
	state *providersState

	// writeMu serialises Add/Update/Remove. Reads keep going through mu;
	// keeping a separate write mutex avoids holding the read mutex across
	// the (possibly slow) atomic file rename in WriteProvidersFile.
	writeMu sync.Mutex
}

type providersState struct {
	providers map[string]*ProviderConfig
	mtime     time.Time
}

// ProvidersOption configures a ProviderRegistry at construction time.
type ProvidersOption func(*ProviderRegistry)

// WithProvidersLogger attaches a logger so reload warnings (parse errors
// after a successful initial load, transient stat failures) are visible.
// Defaults to zap.NewNop(). Named long to disambiguate from
// pool.WithLogger which lives in the same package.
func WithProvidersLogger(l *zap.Logger) ProvidersOption {
	return func(r *ProviderRegistry) {
		if l != nil {
			r.logger = l
		}
	}
}

// withReadFile injects a fake os.ReadFile for tests that count
// reloads. Unexported on purpose — production code goes through
// os.ReadFile and never sees this seam.
func withReadFile(fn func(string) ([]byte, error)) ProvidersOption {
	return func(r *ProviderRegistry) { r.readFile = fn }
}

func LoadProviders(opts ...ProvidersOption) (*ProviderRegistry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(home, ".otters", "providers.yaml")

	return LoadProvidersFrom(path, opts...)
}

func LoadProvidersFrom(path string, opts ...ProvidersOption) (*ProviderRegistry, error) {
	r := &ProviderRegistry{
		path:     path,
		logger:   zap.NewNop(),
		readFile: os.ReadFile,
		state:    emptyProvidersState(),
	}

	for _, opt := range opts {
		opt(r)
	}

	// Eager load so daemon startup surfaces a malformed file immediately.
	// A missing file is valid (empty registry); other errors propagate.
	if err := r.refresh(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	return r, nil
}

func emptyProvidersState() *providersState {
	return &providersState{providers: make(map[string]*ProviderConfig)}
}

// refresh reads the file when its mtime has advanced and swaps in a
// fresh state. Idempotent: callers may invoke it on every read. A
// missing file collapses to an empty state — letting the user
// un-configure providers without restarting the daemon.
func (r *ProviderRegistry) refresh() error {
	info, err := os.Stat(r.path)
	if errors.Is(err, fs.ErrNotExist) {
		r.swap(emptyProvidersState())

		return nil
	}

	if err != nil {
		return fmt.Errorf("stat providers file: %w", err)
	}

	mtime := info.ModTime()

	r.mu.RLock()
	current := r.state.mtime
	r.mu.RUnlock()

	if current.Equal(mtime) {
		return nil
	}

	data, err := r.readFile(r.path)
	if err != nil {
		return fmt.Errorf("reading providers file: %w", err)
	}

	var file ProvidersFile
	if err = yaml.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("parsing providers file: %w", err)
	}

	next := &providersState{
		providers: make(map[string]*ProviderConfig, len(file.Providers)),
		mtime:     mtime,
	}

	for i := range file.Providers {
		p := &file.Providers[i]
		p.APIKey = os.ExpandEnv(p.APIKey)
		p.APIBase = os.ExpandEnv(p.APIBase)
		next.providers[p.Name] = p
	}

	r.swap(next)

	return nil
}

func (r *ProviderRegistry) swap(s *providersState) {
	r.mu.Lock()
	r.state = s
	r.mu.Unlock()
}

// snapshot returns the current immutable state, refreshing if the file
// has changed. Refresh failures after the initial load are demoted to
// a warning so a malformed save can't take the daemon down — the user
// keeps the last-known-good config until the next valid save.
func (r *ProviderRegistry) snapshot() *providersState {
	if err := r.refresh(); err != nil {
		r.logger.Warn("provider config refresh failed; using last-known state",
			zap.String("path", r.path), zap.Error(err))
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.state
}

func (r *ProviderRegistry) Count() int {
	return len(r.snapshot().providers)
}

// Each invokes fn for every registered provider in insertion-order-
// independent fashion. Callers that need deterministic iteration
// (e.g. for logging) should sort the result themselves.
func (r *ProviderRegistry) Each(fn func(*ProviderConfig)) {
	for _, p := range r.snapshot().providers {
		fn(p)
	}
}

func (r *ProviderRegistry) ModelAvailable(model string) bool {
	providerName, modelName := splitModel(model)

	p, ok := r.snapshot().providers[providerName]
	if !ok {
		return false
	}

	if len(p.Models) == 0 {
		return true
	}

	return contains(p.Models, modelName)
}

// Resolve returns (apiBase, apiKey, error) for the given fully-qualified
// model ("anthropic/claude-sonnet-4-..."). Order matches agentfile's
// model.Resolver contract (apiURL first), so daemon code can hand this
// method directly to system.WithModelResolver.
func (r *ProviderRegistry) Resolve(model string) (string, string, error) {
	providerName, modelName := splitModel(model)

	p, ok := r.snapshot().providers[providerName]
	if !ok {
		return "", "", fmt.Errorf("provider %q not configured in ~/.otters/providers.yaml", providerName)
	}

	if len(p.Models) > 0 && !contains(p.Models, modelName) {
		return "", "", fmt.Errorf(
			"model %q not available for provider %q (available: %s)",
			modelName, providerName, strings.Join(p.Models, ", "),
		)
	}

	return p.APIBase, p.APIKey, nil
}

func splitModel(model string) (string, string) {
	if idx := strings.Index(model, "/"); idx > 0 {
		return model[:idx], model[idx+1:]
	}

	return model, ""
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}

	return false
}
