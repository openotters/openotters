// White-box test: refresh / snapshot / readFile seam are unexported on
// purpose; promoting them widens the public API for one assertion.
//
//nolint:testpackage // intentional white-box access to private helpers
package internal

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// writeYAML stages a providers.yaml at path with optional mtime
// override. Bumping mtime past the host filesystem's resolution is
// load-bearing: APFS / ext4 round to a whole second on some kernels,
// so two writes within the same second look stale to refresh().
func writeYAML(t *testing.T, path, body string, mtime time.Time) {
	t.Helper()

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}
}

const yamlV1 = `
providers:
  - name: anthropic
    api-key: key-v1
    models:
      - claude-sonnet-4-5
`

const yamlV2 = `
providers:
  - name: anthropic
    api-key: key-v2
    models:
      - claude-sonnet-4-5
`

func TestProviders_PicksUpFileMutationsBetweenCalls(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "providers.yaml")

	t0 := time.Now().Add(-2 * time.Second).Truncate(time.Second)
	writeYAML(t, path, yamlV1, t0)

	r, err := LoadProvidersFrom(path)
	if err != nil {
		t.Fatalf("LoadProvidersFrom: %v", err)
	}

	_, key, err := r.Resolve("anthropic/claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("Resolve v1: %v", err)
	}

	if key != "key-v1" {
		t.Fatalf("Resolve v1 = %q, want key-v1", key)
	}

	// Write v2 with a strictly-greater mtime so refresh() sees it as
	// new even on filesystems with second-grained mtime.
	writeYAML(t, path, yamlV2, t0.Add(time.Minute))

	_, key, err = r.Resolve("anthropic/claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("Resolve v2: %v", err)
	}

	if key != "key-v2" {
		t.Fatalf("Resolve v2 = %q, want key-v2 (registry did not pick up file mutation)", key)
	}
}

func TestProviders_KeepsLastStateOnParseError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "providers.yaml")

	t0 := time.Now().Add(-2 * time.Second).Truncate(time.Second)
	writeYAML(t, path, yamlV1, t0)

	core, recorded := observer.New(zap.WarnLevel)
	r, err := LoadProvidersFrom(path, WithProvidersLogger(zap.New(core)))
	if err != nil {
		t.Fatalf("LoadProvidersFrom: %v", err)
	}

	// Corrupt the file. Bump mtime so refresh() will try to re-parse.
	writeYAML(t, path, "this is not: : valid yaml\n  - [", t0.Add(time.Minute))

	_, key, err := r.Resolve("anthropic/claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("Resolve after corruption: %v", err)
	}

	if key != "key-v1" {
		t.Fatalf("Resolve after corruption = %q, want previous key-v1", key)
	}

	// Exactly one warning: "provider config refresh failed".
	if got := recorded.Len(); got != 1 {
		t.Fatalf("warning count = %d, want 1; entries: %v", got, recorded.All())
	}
}

func TestProviders_TransitionsToEmptyOnFileDelete(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "providers.yaml")

	writeYAML(t, path, yamlV1, time.Time{})

	r, err := LoadProvidersFrom(path)
	if err != nil {
		t.Fatalf("LoadProvidersFrom: %v", err)
	}

	if got := r.Count(); got != 1 {
		t.Fatalf("Count before delete = %d, want 1", got)
	}

	if rmErr := os.Remove(path); rmErr != nil {
		t.Fatalf("Remove: %v", rmErr)
	}

	if got := r.Count(); got != 0 {
		t.Fatalf("Count after delete = %d, want 0", got)
	}

	if _, _, err = r.Resolve("anthropic/claude-sonnet-4-5"); err == nil {
		t.Fatal("Resolve after delete = nil, want provider-not-configured error")
	}

	// Restore: re-create with a fresh mtime so refresh() picks it up.
	writeYAML(t, path, yamlV1, time.Now().Add(time.Second))

	if got := r.Count(); got != 1 {
		t.Fatalf("Count after restore = %d, want 1", got)
	}
}

func TestProviders_NoReloadWhenMtimeUnchanged(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "providers.yaml")

	t0 := time.Now().Add(-time.Second).Truncate(time.Second)
	writeYAML(t, path, yamlV1, t0)

	var reads atomic.Int64
	counted := func(p string) ([]byte, error) {
		reads.Add(1)

		return os.ReadFile(p)
	}

	r, err := LoadProvidersFrom(path, withReadFile(counted))
	if err != nil {
		t.Fatalf("LoadProvidersFrom: %v", err)
	}

	// Eager load = 1 read. Two more Resolve calls without mtime change
	// must not trigger additional reads (mtime fast-path).
	_, _, _ = r.Resolve("anthropic/claude-sonnet-4-5")
	_, _, _ = r.Resolve("anthropic/claude-sonnet-4-5")

	if got := reads.Load(); got != 1 {
		t.Fatalf("ReadFile call count = %d, want 1 (mtime fast-path failed)", got)
	}
}

func TestProviders_LoadProvidersFromMissingFileIsEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.yaml")

	r, err := LoadProvidersFrom(path)
	if err != nil {
		t.Fatalf("LoadProvidersFrom missing = %v, want nil", err)
	}

	if got := r.Count(); got != 0 {
		t.Fatalf("Count = %d, want 0", got)
	}
}

func TestProviders_LoadProvidersFromMalformedFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "providers.yaml")

	writeYAML(t, path, "this: is: not: valid", time.Time{})

	_, err := LoadProvidersFrom(path)
	if err == nil {
		t.Fatal("LoadProvidersFrom malformed = nil, want parse error")
	}

	// The eager-load contract: malformed file at startup fails fast.
	if !errorContains(err, "parsing providers file") {
		t.Fatalf("err = %v, want it to wrap parse failure", err)
	}
}

func errorContains(err error, sub string) bool {
	if err == nil {
		return false
	}

	for e := err; e != nil; e = errors.Unwrap(e) {
		if msg := e.Error(); msg != "" && containsSubstring(msg, sub) {
			return true
		}
	}

	return false
}

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}

	return false
}
