//nolint:testpackage // exercises the file-shape contract via the package's own types
package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadProvidersFile_MissingIsEmpty(t *testing.T) {
	t.Parallel()

	file, err := ReadProvidersFile(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Read missing = %v, want nil", err)
	}

	if len(file.Providers) != 0 {
		t.Fatalf("missing file produced %d providers, want 0", len(file.Providers))
	}
}

func TestProvidersFile_RoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "providers.yaml")

	in := ProvidersFile{
		Providers: []ProviderConfig{
			{Name: "anthropic", APIKey: "sk-ant-fixture", APIBase: "https://api.anthropic.com"},
			{Name: "openai", APIKey: "sk-openai", Models: []string{"gpt-4o", "gpt-4o-mini"}},
		},
	}

	if err := WriteProvidersFile(path, in); err != nil {
		t.Fatalf("Write: %v", err)
	}

	out, err := ReadProvidersFile(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if got, want := len(out.Providers), len(in.Providers); got != want {
		t.Fatalf("provider count = %d, want %d", got, want)
	}

	for i, p := range in.Providers {
		got := out.Providers[i]
		if got.Name != p.Name || got.APIKey != p.APIKey || got.APIBase != p.APIBase {
			t.Errorf("entry %d round-trip mismatch:\n got  %+v\n want %+v", i, got, p)
		}

		if len(got.Models) != len(p.Models) {
			t.Errorf("entry %d models len = %d, want %d", i, len(got.Models), len(p.Models))
		}
	}
}

func TestWriteProvidersFile_AtomicReplace(t *testing.T) {
	t.Parallel()

	// Pre-existing file must survive an aborted write — atomic-rename
	// semantics. We can't easily simulate a crash mid-write, but we
	// can assert that the on-disk file is exactly the marshalled
	// content (no leftover tmp files in the directory).
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.yaml")

	if err := WriteProvidersFile(path, ProvidersFile{
		Providers: []ProviderConfig{{Name: "x", APIKey: "k1"}},
	}); err != nil {
		t.Fatalf("Write 1: %v", err)
	}

	if err := WriteProvidersFile(path, ProvidersFile{
		Providers: []ProviderConfig{{Name: "y", APIKey: "k2"}},
	}); err != nil {
		t.Fatalf("Write 2: %v", err)
	}

	out, err := ReadProvidersFile(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if len(out.Providers) != 1 || out.Providers[0].Name != "y" {
		t.Fatalf("post-rewrite content = %+v, want only 'y'", out.Providers)
	}

	// No leaked temp file (.providers.*.yaml) in the directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	for _, e := range entries {
		if e.Name() != "providers.yaml" {
			t.Errorf("unexpected leftover %q in providers dir", e.Name())
		}
	}
}

func TestWriteProvidersFile_PermissionsAreRestrictive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "providers.yaml")

	if err := WriteProvidersFile(path, ProvidersFile{
		Providers: []ProviderConfig{{Name: "x", APIKey: "secret"}},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	// Bottom 9 perm bits should be 0600 — file may carry plaintext
	// secrets, so other / group must not read.
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("perm = %o, want 0600", got)
	}
}

func TestUpsertProvider_AppendsAndReplaces(t *testing.T) {
	t.Parallel()

	file := ProvidersFile{}

	if replaced := UpsertProvider(&file, ProviderConfig{Name: "a", APIKey: "k1"}); replaced {
		t.Fatal("first upsert reported replaced=true on empty list")
	}

	if replaced := UpsertProvider(&file, ProviderConfig{Name: "b", APIKey: "k2"}); replaced {
		t.Fatal("appending fresh name reported replaced=true")
	}

	if replaced := UpsertProvider(&file, ProviderConfig{Name: "a", APIKey: "k1-new"}); !replaced {
		t.Fatal("upserting existing name reported replaced=false")
	}

	if got := len(file.Providers); got != 2 {
		t.Fatalf("provider count = %d, want 2", got)
	}

	a := FindProvider(file, "a")
	if a == nil {
		t.Fatal("FindProvider(a) = nil")
	}

	if a.APIKey != "k1-new" {
		t.Fatalf("APIKey after replace = %q, want k1-new", a.APIKey)
	}
}

func TestRemoveProviders_DropsByName(t *testing.T) {
	t.Parallel()

	file := ProvidersFile{
		Providers: []ProviderConfig{
			{Name: "a"}, {Name: "b"}, {Name: "c"},
		},
	}

	removed := RemoveProviders(&file, []string{"a", "c", "missing"})

	if removed != 2 {
		t.Fatalf("removed = %d, want 2 (missing entry ignored)", removed)
	}

	if len(file.Providers) != 1 || file.Providers[0].Name != "b" {
		t.Fatalf("remaining = %+v, want [b]", file.Providers)
	}
}

func TestFindProvider_Misses(t *testing.T) {
	t.Parallel()

	file := ProvidersFile{Providers: []ProviderConfig{{Name: "a"}}}

	if FindProvider(file, "missing") != nil {
		t.Fatal("FindProvider returned non-nil for missing name")
	}
}
