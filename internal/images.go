package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"go.uber.org/zap"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// MaxRegistryReadBytes is the cap applied to every registry-side
// read. 100 MiB is comfortably above any realistic OCI manifest
// (a few KiB at most) and most BIN-tool blob layers (single static
// binaries, typically <50 MiB stripped). Override on the Daemon if
// you need to ship multi-hundred-MiB layers; the override surfaces
// through Daemon.MaxRegistryReadBytes when non-zero.
//
//nolint:gochecknoglobals // module-wide tunable, used as a constant
var MaxRegistryReadBytes int64 = 100 * 1024 * 1024

// readAllCapped is io.ReadAll with a hard byte cap so a malicious or
// confused registry can't OOM the daemon by streaming an unbounded
// body. Returns an error when the body is at least limit+1 bytes long.
func readAllCapped(r io.Reader) ([]byte, error) {
	limit := MaxRegistryReadBytes

	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}

	if int64(len(data)) > limit {
		return nil, fmt.Errorf("registry response exceeds %d-byte cap", limit)
	}

	return data, nil
}

func (d *Daemon) ListImages(
	ctx context.Context, _ *daemonv1.ListImagesRequest,
) (*daemonv1.ListImagesResponse, error) {
	addr := d.registry.Addr()

	catalog, err := fetchJSON[catalogResponse](ctx, fmt.Sprintf("http://%s/v2/_catalog", addr))
	if err != nil {
		return nil, fmt.Errorf("listing repositories: %w", err)
	}

	var images []*daemonv1.ImageInfo

	for _, repo := range catalog.Repositories {
		tags, tagsErr := fetchJSON[tagsResponse](ctx,
			fmt.Sprintf("http://%s/v2/%s/tags/list", addr, repo),
		)
		if tagsErr != nil {
			continue
		}

		for _, tag := range tags.Tags {
			ref := repo + ":" + tag

			manifest, manifestErr := fetchManifestInfo(ctx, addr, repo, tag)
			if manifestErr != nil {
				continue
			}

			images = append(images, &daemonv1.ImageInfo{
				Ref:          ref,
				Digest:       manifest.digest,
				ArtifactType: manifest.artifactType,
				Size:         manifest.size,
			})
		}
	}

	return &daemonv1.ListImagesResponse{Images: images}, nil
}

func (d *Daemon) RemoveImage(
	ctx context.Context, req *daemonv1.RemoveImageRequest,
) (*daemonv1.RemoveImageResponse, error) {
	addr := d.registry.Addr()
	ref := req.GetRef()

	repo, tag := splitRef(ref)

	manifest, err := fetchManifestInfo(ctx, addr, repo, tag)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", ref, err)
	}

	for _, deleteRef := range []string{tag, manifest.digest} {
		deleteURL := fmt.Sprintf("http://%s/v2/%s/manifests/%s", addr, repo, deleteRef)

		httpReq, reqErr := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
		if reqErr != nil {
			return nil, reqErr
		}

		resp, doErr := http.DefaultClient.Do(httpReq)
		if doErr != nil {
			return nil, fmt.Errorf("deleting %s: %w", ref, doErr)
		}
		resp.Body.Close()
	}

	d.logger.Info("image removed", zap.String("ref", ref))

	return &daemonv1.RemoveImageResponse{}, nil
}

func (d *Daemon) DescribeImage(
	ctx context.Context, req *daemonv1.DescribeImageRequest,
) (*daemonv1.DescribeImageResponse, error) {
	addr := d.registry.Addr()
	ref := req.GetRef()

	repo, tag := splitRef(ref)

	manifestData, digest, err := fetchManifestRaw(ctx, addr, repo, tag)
	if err != nil {
		return nil, fmt.Errorf("fetching manifest for %s: %w", ref, err)
	}

	var manifest v1.Manifest
	if err = json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}

	configData, err := fetchBlob(ctx, addr, repo, manifest.Config.Digest.String())
	if err != nil {
		return nil, fmt.Errorf("fetching config: %w", err)
	}

	var layers []string
	for _, l := range manifest.Layers {
		title := l.Annotations["org.opencontainers.image.title"]
		if title == "" {
			title = l.Digest.String()[:16]
		}

		layers = append(layers, fmt.Sprintf("%s (%s, %d bytes)", title, l.MediaType, l.Size))
	}

	return &daemonv1.DescribeImageResponse{
		Ref:          ref,
		Digest:       digest,
		ArtifactType: manifest.ArtifactType,
		Config:       string(configData),
		Layers:       layers,
		Labels:       manifest.Annotations,
	}, nil
}

type catalogResponse struct {
	Repositories []string `json:"repositories"`
}

type tagsResponse struct {
	Tags []string `json:"tags"`
}

type manifestInfo struct {
	digest       string
	artifactType string
	size         int64
}

func fetchManifestInfo(ctx context.Context, addr, repo, tag string) (*manifestInfo, error) {
	data, digest, err := fetchManifestRaw(ctx, addr, repo, tag)
	if err != nil {
		return nil, err
	}

	// Try as a multi-arch index first: if it decodes with a non-empty
	// manifests array, sum the size of every platform submanifest plus
	// the index document itself. Otherwise treat it as a plain manifest
	// and sum config + all layer sizes.
	var index v1.Index
	if json.Unmarshal(data, &index) == nil && len(index.Manifests) > 0 {
		total := int64(len(data))

		for _, m := range index.Manifests {
			sub, subErr := fetchManifestInfo(ctx, addr, repo, m.Digest.String())
			if subErr != nil {
				total += m.Size

				continue
			}

			total += sub.size
		}

		return &manifestInfo{
			digest:       digest,
			artifactType: index.ArtifactType,
			size:         total,
		}, nil
	}

	var manifest v1.Manifest
	if parseErr := json.Unmarshal(data, &manifest); parseErr != nil {
		return nil, fmt.Errorf("parsing manifest: %w", parseErr)
	}

	total := int64(len(data)) + manifest.Config.Size
	for _, l := range manifest.Layers {
		total += l.Size
	}

	return &manifestInfo{
		digest:       digest,
		artifactType: manifest.ArtifactType,
		size:         total,
	}, nil
}

func fetchManifestRaw(ctx context.Context, addr, repo, tag string) ([]byte, string, error) {
	url := fmt.Sprintf("http://%s/v2/%s/manifests/%s", addr, repo, tag)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}

	req.Header.Set("Accept", v1.MediaTypeImageManifest)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("manifest %s/%s: HTTP %d", repo, tag, resp.StatusCode)
	}

	data, err := readAllCapped(resp.Body)
	if err != nil {
		return nil, "", err
	}

	return data, resp.Header.Get("Docker-Content-Digest"), nil
}

func fetchBlob(ctx context.Context, addr, repo, digest string) ([]byte, error) {
	url := fmt.Sprintf("http://%s/v2/%s/blobs/%s", addr, repo, digest)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return readAllCapped(resp.Body)
}

func fetchJSON[T any](ctx context.Context, url string) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result T
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func splitRef(ref string) (string, string) {
	if idx := strings.LastIndex(ref, ":"); idx > 0 {
		return ref[:idx], ref[idx+1:]
	}

	return ref, "latest"
}
