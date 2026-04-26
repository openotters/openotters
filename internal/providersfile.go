package internal

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultProvidersPath returns the canonical on-disk location of the
// providers config (~/.otters/providers.yaml). Used by both the daemon
// (LoadProviders) and the CLI (provider add/rm/ls) so they agree on
// the same file.
func DefaultProvidersPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, ".otters", "providers.yaml"), nil
}

// ReadProvidersFile loads the on-disk file as a plain ProvidersFile —
// no env expansion, no registry semantics. The CLI uses this to
// inspect or mutate the file before writing it back. A missing file
// returns an empty ProvidersFile + nil error so `provider add` can
// create it on first use.
func ReadProvidersFile(path string) (ProvidersFile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ProvidersFile{}, nil
	}

	if err != nil {
		return ProvidersFile{}, fmt.Errorf("reading providers file: %w", err)
	}

	var file ProvidersFile
	if err = yaml.Unmarshal(data, &file); err != nil {
		return ProvidersFile{}, fmt.Errorf("parsing providers file: %w", err)
	}

	return file, nil
}

// WriteProvidersFile serialises file to path atomically: writes to a
// sibling tmp file then renames. A crash mid-write leaves the previous
// version intact rather than producing a half-written YAML the daemon
// would refuse to parse.
func WriteProvidersFile(path string, file ProvidersFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating providers dir: %w", err)
	}

	data, err := yaml.Marshal(file)
	if err != nil {
		return fmt.Errorf("marshaling providers file: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".providers.*.yaml")
	if err != nil {
		return fmt.Errorf("creating tmp file: %w", err)
	}

	tmpPath := tmp.Name()

	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, writeErr := tmp.Write(data); writeErr != nil {
		_ = tmp.Close()

		cleanup()

		return fmt.Errorf("writing tmp providers file: %w", writeErr)
	}

	if closeErr := tmp.Close(); closeErr != nil {
		cleanup()

		return fmt.Errorf("closing tmp providers file: %w", closeErr)
	}

	// Match the canonical 0600 — the file may carry plaintext API keys
	// when the user pastes them directly (rather than $ENV references).
	if chmodErr := os.Chmod(tmpPath, 0o600); chmodErr != nil {
		cleanup()

		return fmt.Errorf("chmod tmp providers file: %w", chmodErr)
	}

	if renameErr := os.Rename(tmpPath, path); renameErr != nil {
		cleanup()

		return fmt.Errorf("rename providers file: %w", renameErr)
	}

	return nil
}

// FindProvider returns a pointer to the provider with the given name
// in file.Providers, or nil. Comparison is case-sensitive; provider
// names are required to be lowercase.
func FindProvider(file ProvidersFile, name string) *ProviderConfig {
	for i := range file.Providers {
		if file.Providers[i].Name == name {
			return &file.Providers[i]
		}
	}

	return nil
}

// UpsertProvider inserts cfg into file.Providers, replacing any
// existing entry with the same name. Returns true if an existing
// entry was replaced (lets the CLI report "added" vs "updated").
func UpsertProvider(file *ProvidersFile, cfg ProviderConfig) bool {
	for i := range file.Providers {
		if file.Providers[i].Name == cfg.Name {
			file.Providers[i] = cfg

			return true
		}
	}

	file.Providers = append(file.Providers, cfg)

	return false
}

// RemoveProviders drops every provider whose name is in names from
// file.Providers and returns the count actually removed. Unknown
// names are silently ignored — the caller has already shown the user
// the list to choose from.
func RemoveProviders(file *ProvidersFile, names []string) int {
	if len(names) == 0 {
		return 0
	}

	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		want[n] = struct{}{}
	}

	kept := file.Providers[:0]
	removed := 0

	for _, p := range file.Providers {
		if _, drop := want[p.Name]; drop {
			removed++

			continue
		}

		kept = append(kept, p)
	}

	file.Providers = kept

	return removed
}
