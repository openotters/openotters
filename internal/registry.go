package internal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
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

type EmbeddedRegistry struct {
	addr    string
	dataDir string
	server  *http.Server
	logger  *zap.Logger
}

func NewEmbeddedRegistry(logger *zap.Logger) *EmbeddedRegistry {
	return &EmbeddedRegistry{
		logger: logger.Named("registry"),
	}
}

func (r *EmbeddedRegistry) Start() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	r.dataDir = filepath.Join(home, ".openotters", "registry")
	blobDir := filepath.Join(r.dataDir, "blobs")

	if err = os.MkdirAll(blobDir, 0o755); err != nil {
		return fmt.Errorf("creating registry dir: %w", err)
	}

	manifests := newDiskManifestStore(filepath.Join(r.dataDir, "manifests"))

	inner := registry.New(registry.WithBlobHandler(registry.NewDiskBlobHandler(blobDir)))
	handler := &persistentHandler{inner: inner, manifests: manifests}

	lc := net.ListenConfig{}

	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listening: %w", err)
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

	// Also forward to inner handler so blob existence checks work
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	h.inner.ServeHTTP(w, r)
}

func (h *persistentHandler) handleManifestGet(w http.ResponseWriter, r *http.Request) bool {
	repo, ref := parseManifestPath(r.URL.Path)

	data, ok := h.manifests.get(repo, ref)
	if !ok {
		return false
	}

	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.WriteHeader(http.StatusOK)

		return true
	}

	w.WriteHeader(http.StatusOK)
	w.Write(data) //nolint:errcheck // best-effort

	return true
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

func (s *diskManifestStore) listRepos() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}

	var repos []string
	for _, e := range entries {
		if e.IsDir() {
			repos = append(repos, e.Name())
		}
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

	return io.ReadAll(r.Body)
}
