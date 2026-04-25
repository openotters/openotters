package internal

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	stdlog "log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	"go.uber.org/zap"
)

// DefaultRegistryAddr is the loopback TCP endpoint the embedded registry
// binds on by default. Fixed (not ephemeral) so the address surviving in
// persisted agent refs stays valid across daemon restarts. Callers can
// override with the OTTERS_REGISTRY_ADDR env var if the port clashes.
const DefaultRegistryAddr = "127.0.0.1:5527"

type EmbeddedRegistry struct {
	addr     string
	bindAddr string
	dataDir  string
	server   *http.Server
	logger   *zap.Logger
}

// RegistryOption configures an EmbeddedRegistry at construction.
type RegistryOption func(*EmbeddedRegistry)

// WithRegistryAddr overrides the TCP bind address. Precedence is
// option > OTTERS_REGISTRY_ADDR env var > DefaultRegistryAddr; pass an
// empty string to fall through to the env/default.
func WithRegistryAddr(addr string) RegistryOption {
	return func(r *EmbeddedRegistry) {
		if addr != "" {
			r.bindAddr = addr
		}
	}
}

func NewEmbeddedRegistry(logger *zap.Logger, opts ...RegistryOption) *EmbeddedRegistry {
	bind := os.Getenv("OTTERS_REGISTRY_ADDR")
	if bind == "" {
		bind = DefaultRegistryAddr
	}

	r := &EmbeddedRegistry{
		logger:   logger.Named("registry"),
		bindAddr: bind,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

func (r *EmbeddedRegistry) Start(ctx context.Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	r.dataDir = filepath.Join(home, ".otters", "registry")
	blobDir := filepath.Join(r.dataDir, "blobs")

	if err = os.MkdirAll(blobDir, 0o755); err != nil {
		return fmt.Errorf("creating registry dir: %w", err)
	}

	manifests := newDiskManifestStore(filepath.Join(r.dataDir, "manifests"))

	inner := registry.New(
		registry.WithBlobHandler(registry.NewDiskBlobHandler(blobDir)),
		registry.Logger(stdlog.New(&zapWriter{logger: r.logger}, "", 0)),
	)
	handler := &persistentHandler{inner: inner, manifests: manifests}

	lc := net.ListenConfig{}

	lis, err := lc.Listen(ctx, "tcp", r.bindAddr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", r.bindAddr, err)
	}

	r.addr = lis.Addr().String()
	r.server = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if serveErr := r.server.Serve(lis); serveErr != nil && serveErr != http.ErrServerClosed {
			r.logger.Error("registry server failed", zap.Error(serveErr))
		}
	}()

	r.logger.Info("embedded registry started", zap.String("addr", r.addr))

	return nil
}

func (r *EmbeddedRegistry) Addr() string {
	return r.addr
}

func (r *EmbeddedRegistry) Stop() {
	if r.server != nil {
		r.server.Close()
	}
}

type persistentHandler struct {
	inner     http.Handler
	manifests *diskManifestStore
}

func (h *persistentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isManifestRequest(r.URL.Path) {
		switch r.Method {
		case http.MethodPut:
			h.handleManifestPut(w, r)

			return
		case http.MethodGet, http.MethodHead:
			if h.handleManifestGet(w, r) {
				return
			}
		case http.MethodDelete:
			h.handleManifestDelete(w, r)

			return
		}
	}

	if r.URL.Path == "/v2/_catalog" {
		h.handleCatalog(w)

		return
	}

	if isTagsRequest(r.URL.Path) {
		h.handleTags(w, r)

		return
	}

	h.inner.ServeHTTP(w, r)
}

// handleManifestPut owns manifest writes end-to-end. Earlier we also
// forwarded each PUT to the inner go-containerregistry registry for
// validation, but that forked state: sub-manifests written straight to
// disk (because oras saw them as already-present via HEAD and skipped
// the PUT) never made it into the inner registry's in-memory map, and
// a subsequent index PUT got rejected with MANIFEST_UNKNOWN because
// the inner validator only looks at its own in-memory map. Handling
// the write here keeps disk as the single source of truth for
// manifests; blobs still flow through inner's disk-backed blob store.
func (h *persistentHandler) handleManifestPut(w http.ResponseWriter, r *http.Request) {
	repo, ref := parseManifestPath(r.URL.Path)

	body, err := readBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))

	h.manifests.put(repo, ref, body)

	if ref != digest {
		h.manifests.put(repo, digest, body)
	}

	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Location", fmt.Sprintf("/v2/%s/manifests/%s", repo, digest))
	w.WriteHeader(http.StatusCreated)
}

func (h *persistentHandler) handleManifestGet(w http.ResponseWriter, r *http.Request) bool {
	repo, ref := parseManifestPath(r.URL.Path)

	data, ok := h.manifests.get(repo, ref)
	if !ok {
		return false
	}

	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Type", manifestMediaType(data))

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.WriteHeader(http.StatusOK)

		return true
	}

	w.WriteHeader(http.StatusOK)
	w.Write(data) //nolint:errcheck // best-effort

	return true
}

// manifestMediaType inspects the manifest JSON and returns the Content-Type
// oras clients expect. We read `mediaType` when present; otherwise we
// distinguish index vs manifest by the presence of `manifests` or
// `layers`. Falls back to the single-arch manifest media type so that
// images written by older oras clients (which omit mediaType) still work.
func manifestMediaType(data []byte) string {
	var probe struct {
		MediaType string            `json:"mediaType"`
		Manifests []json.RawMessage `json:"manifests"`
		Layers    []json.RawMessage `json:"layers"`
	}

	if err := json.Unmarshal(data, &probe); err == nil {
		if probe.MediaType != "" {
			return probe.MediaType
		}

		if len(probe.Manifests) > 0 {
			return "application/vnd.oci.image.index.v1+json"
		}

		if len(probe.Layers) > 0 {
			return "application/vnd.oci.image.manifest.v1+json"
		}
	}

	return "application/vnd.oci.image.manifest.v1+json"
}

func (h *persistentHandler) handleManifestDelete(w http.ResponseWriter, r *http.Request) {
	repo, ref := parseManifestPath(r.URL.Path)
	h.manifests.delete(repo, ref)
	w.WriteHeader(http.StatusAccepted)
}

func (h *persistentHandler) handleCatalog(w http.ResponseWriter) {
	repos := h.manifests.listRepos()
	data, _ := json.Marshal(map[string][]string{"repositories": repos})

	w.Header().Set("Content-Type", "application/json")
	w.Write(data) //nolint:errcheck // best-effort
}

func (h *persistentHandler) handleTags(w http.ResponseWriter, r *http.Request) {
	repo := parseTagsPath(r.URL.Path)
	tags := h.manifests.listTags(repo)
	data, _ := json.Marshal(map[string]any{"name": repo, "tags": tags})

	w.Header().Set("Content-Type", "application/json")
	w.Write(data) //nolint:errcheck // best-effort
}

type diskManifestStore struct {
	dir string
	mu  sync.RWMutex
}

func newDiskManifestStore(dir string) *diskManifestStore {
	os.MkdirAll(dir, 0o755) //nolint:errcheck // best-effort

	return &diskManifestStore{dir: dir}
}

func (s *diskManifestStore) put(repo, ref string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(s.dir, repo)
	os.MkdirAll(dir, 0o755) //nolint:errcheck // best-effort

	safe := safeRef(ref)
	os.WriteFile(filepath.Join(dir, safe), data, 0o600) //nolint:errcheck // best-effort
}

func (s *diskManifestStore) get(repo, ref string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(filepath.Join(s.dir, repo, safeRef(ref)))
	if err != nil {
		return nil, false
	}

	return data, true
}

func (s *diskManifestStore) delete(repo, ref string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = os.Remove(filepath.Join(s.dir, repo, safeRef(ref)))
}

// listRepos walks the manifest tree and returns every directory that
// directly contains manifest files. A repo name is the directory's path
// relative to the root — multi-component paths like
// "ghcr.io/openotters/tools/jina" are preserved, so the bin cache
// shows up correctly in /v2/_catalog.
func (s *diskManifestStore) listRepos() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[string]struct{})

	_ = filepath.WalkDir(s.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // skip unreadable entries, keep walking
		}

		rel, relErr := filepath.Rel(s.dir, filepath.Dir(path))
		if relErr != nil || rel == "." {
			return nil //nolint:nilerr // skip top-level / unrelative paths
		}

		seen[rel] = struct{}{}

		return nil
	})

	repos := make([]string, 0, len(seen))
	for r := range seen {
		repos = append(repos, r)
	}

	return repos
}

func (s *diskManifestStore) listTags(repo string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(filepath.Join(s.dir, repo))
	if err != nil {
		return nil
	}

	var tags []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		name := e.Name()
		if !strings.HasPrefix(name, "sha256_") {
			tags = append(tags, name)
		}
	}

	return tags
}

func safeRef(ref string) string {
	return strings.ReplaceAll(ref, ":", "_")
}

func isManifestRequest(path string) bool {
	return strings.Contains(path, "/manifests/")
}

func isTagsRequest(path string) bool {
	return strings.HasSuffix(path, "/tags/list")
}

func parseManifestPath(path string) (string, string) {
	path = strings.TrimPrefix(path, "/v2/")
	idx := strings.Index(path, "/manifests/")

	if idx < 0 {
		return "", ""
	}

	return path[:idx], path[idx+len("/manifests/"):]
}

func parseTagsPath(path string) string {
	path = strings.TrimPrefix(path, "/v2/")

	return strings.TrimSuffix(path, "/tags/list")
}

func readBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()

	return readAllCapped(r.Body)
}

// zapWriter adapts go-containerregistry's *log.Logger output to a
// zap.Logger so the embedded registry's per-request lines land in the
// same structured stream as the rest of the daemon. Each Write
// corresponds to one line from the stdlib logger; we strip the
// trailing newline and forward it as a debug message.
type zapWriter struct {
	logger *zap.Logger
}

func (w *zapWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	if msg != "" {
		w.logger.Debug(msg)
	}

	return len(p), nil
}
