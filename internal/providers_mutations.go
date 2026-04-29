package internal

import (
	"errors"
	"fmt"
	"sort"
)

// Sentinel errors returned by ProviderRegistry mutation methods. Callers
// (notably the Connect RPC handler) translate these into wire-level
// codes — runtime_handler.go's mapProviderError converts them to
// connect.CodeNotFound / CodeAlreadyExists.
var (
	ErrProviderNotFound      = errors.New("provider not found")
	ErrProviderAlreadyExists = errors.New("provider already exists")
)

// Snapshot returns the currently registered providers, env-expanded,
// sorted by name. Use this for read-only listing (RPC ListProviders,
// CLI `otters provider ls`); use Resolve for the model-lookup hot path
// where you only care about one provider.
func (r *ProviderRegistry) Snapshot() []ProviderConfig {
	state := r.snapshot()

	out := make([]ProviderConfig, 0, len(state.providers))
	for _, p := range state.providers {
		out = append(out, *p)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// Add inserts a new provider into ~/.otters/providers.yaml and the
// in-memory cache. Returns ErrProviderAlreadyExists if a provider
// with the same name is already configured. The returned ProviderConfig
// reflects the env-expanded form (api-key with ${VAR} resolved) so
// callers can confirm what the daemon will actually use.
func (r *ProviderRegistry) Add(cfg ProviderConfig) (ProviderConfig, error) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	file, err := ReadProvidersFile(r.path)
	if err != nil {
		return ProviderConfig{}, err
	}

	if FindProvider(file, cfg.Name) != nil {
		return ProviderConfig{}, fmt.Errorf("%w: %s", ErrProviderAlreadyExists, cfg.Name)
	}

	file.Providers = append(file.Providers, cfg)

	if writeErr := WriteProvidersFile(r.path, file); writeErr != nil {
		return ProviderConfig{}, writeErr
	}

	if refreshErr := r.refresh(); refreshErr != nil {
		return ProviderConfig{}, refreshErr
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.state.providers[cfg.Name]
	if !ok {
		// File written, but reload doesn't see it — typically a YAML parse
		// error introduced concurrently. The save itself succeeded; surface
		// the un-expanded request so the caller knows what was persisted.
		return cfg, nil
	}

	return *p, nil
}

// Update replaces an existing provider entry. Returns ErrProviderNotFound
// if the named provider isn't configured. Unlike Add, accepting partial
// patches (only some fields populated) is not supported — pass the full
// desired ProviderConfig. The CLI/web read-modify-write under their own
// optimistic-concurrency assumptions; the daemon doesn't merge.
func (r *ProviderRegistry) Update(cfg ProviderConfig) (ProviderConfig, error) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	file, err := ReadProvidersFile(r.path)
	if err != nil {
		return ProviderConfig{}, err
	}

	if FindProvider(file, cfg.Name) == nil {
		return ProviderConfig{}, fmt.Errorf("%w: %s", ErrProviderNotFound, cfg.Name)
	}

	UpsertProvider(&file, cfg)

	if writeErr := WriteProvidersFile(r.path, file); writeErr != nil {
		return ProviderConfig{}, writeErr
	}

	if refreshErr := r.refresh(); refreshErr != nil {
		return ProviderConfig{}, refreshErr
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.state.providers[cfg.Name]
	if !ok {
		return cfg, nil
	}

	return *p, nil
}

// Remove drops the named provider. Returns ErrProviderNotFound if the
// provider isn't configured.
func (r *ProviderRegistry) Remove(name string) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	file, err := ReadProvidersFile(r.path)
	if err != nil {
		return err
	}

	if FindProvider(file, name) == nil {
		return fmt.Errorf("%w: %s", ErrProviderNotFound, name)
	}

	RemoveProviders(&file, []string{name})

	if writeErr := WriteProvidersFile(r.path, file); writeErr != nil {
		return writeErr
	}

	return r.refresh()
}
