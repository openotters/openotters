package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

type ProviderRegistry struct {
	providers map[string]*ProviderConfig
}

func LoadProviders() (*ProviderRegistry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(home, ".otters", "providers.yaml")

	return LoadProvidersFrom(path)
}

func LoadProvidersFrom(path string) (*ProviderRegistry, error) {
	reg := &ProviderRegistry{providers: make(map[string]*ProviderConfig)}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return reg, nil
		}

		return nil, fmt.Errorf("reading providers file: %w", err)
	}

	var file ProvidersFile
	if err = yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing providers file: %w", err)
	}

	for i := range file.Providers {
		p := &file.Providers[i]
		p.APIKey = os.ExpandEnv(p.APIKey)
		p.APIBase = os.ExpandEnv(p.APIBase)
		reg.providers[p.Name] = p
	}

	return reg, nil
}

func (r *ProviderRegistry) Count() int {
	return len(r.providers)
}

// Each invokes fn for every registered provider in insertion-order-
// independent fashion. Callers that need deterministic iteration
// (e.g. for logging) should sort the result themselves.
func (r *ProviderRegistry) Each(fn func(*ProviderConfig)) {
	for _, p := range r.providers {
		fn(p)
	}
}

func (r *ProviderRegistry) ModelAvailable(model string) bool {
	providerName, modelName := splitModel(model)

	p, ok := r.providers[providerName]
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

	p, ok := r.providers[providerName]
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
