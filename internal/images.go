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
	// Reads exclusively from the daemon's images cache — no docker
	// round trip per call. Cache is populated at every ingestion
	// site (build / pull / save / push) and reconciled on demand
	// via RefreshImages.
	rows, err := d.state.ListImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing images: %w", err)
	}

	images := make([]*daemonv1.ImageInfo, 0, len(rows))
	for _, r := range rows {
		images = append(images, &daemonv1.ImageInfo{
			Ref:          r.Ref,
			Digest:       r.Digest,
			ArtifactType: r.ArtifactType,
			Size:         r.Size,
			CreatedAt:    r.CreatedUnix,
			Description:  r.Description,
			Source:       r.Source,
		})
	}

	return &daemonv1.ListImagesResponse{Images: images}, nil
}

// RefreshImage re-reads a single ref's metadata from the executor
// registry and updates the cache row. Used by the dashboard's
// per-image Refresh button when an operator suspects a row is
// stale (e.g. they pushed a new digest under the same tag from
// another tool, or the docker store changed under the daemon).
//
// Carries forward the cache's existing artifact_type when the
// registry can't surface a fresh kind (docker's ManifestKind
// always returns empty), so refresh on docker doesn't downgrade a
// previously-built kind.
func (d *Daemon) RefreshImage(
	ctx context.Context, req *daemonv1.RefreshImageRequest,
) (*daemonv1.RefreshImageResponse, error) {
	ref := req.GetRef()
	if ref == "" {
		return nil, fmt.Errorf("ref is required")
	}

	reg := d.pool.provider.Registry()

	info, err := reg.Inspect(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("inspecting %s: %w", ref, err)
	}

	kind, _ := reg.ManifestKind(ctx, ref)

	if kind == "" {
		// Carry forward whatever kind the existing cache row knew
		// for this digest — the docker backend's ManifestKind is
		// a no-op and we don't want a refresh to blank out a
		// build-supplied value.
		existing, listErr := d.state.ListImages(ctx)
		if listErr == nil {
			for _, e := range existing {
				if e.Digest == info.Digest && e.ArtifactType != "" {
					kind = e.ArtifactType

					break
				}
			}
		}
	}

	if err = d.state.UpsertImage(ctx, PersistedImage{
		Ref:          ref,
		Digest:       info.Digest,
		ArtifactType: kind,
		Size:         info.Size,
		CreatedUnix:  info.CreatedUnix,
		Description:  info.Description,
		Source:       info.Source,
	}); err != nil {
		return nil, fmt.Errorf("upsert %s: %w", ref, err)
	}

	d.logger.Info("image refreshed",
		zap.String("ref", ref),
		zap.String("digest", info.Digest),
		zap.String("artifact_type", kind))

	return &daemonv1.RefreshImageResponse{
		Ref:          ref,
		Digest:       info.Digest,
		ArtifactType: kind,
	}, nil
}

func (d *Daemon) RemoveImage(
	ctx context.Context, req *daemonv1.RemoveImageRequest,
) (*daemonv1.RemoveImageResponse, error) {
	ref := req.GetRef()
	reg := d.pool.provider.Registry()

	// Capture the digest before removing — docker's ImageRemove
	// untags every alias of the underlying ID, so we want to drop
	// every cache row for that digest in one go.
	var digest string
	if info, err := reg.Inspect(ctx, ref); err == nil {
		digest = info.Digest
	}

	if err := reg.Remove(ctx, ref); err != nil {
		return nil, fmt.Errorf("removing %s: %w", ref, err)
	}

	if digest != "" {
		if err := d.state.DeleteImagesByDigest(ctx, digest); err != nil {
			d.logger.Warn("removing images cache rows",
				zap.String("digest", digest), zap.Error(err))
		}
	} else if err := d.state.DeleteImageByRef(ctx, ref); err != nil {
		d.logger.Warn("removing images cache row",
			zap.String("ref", ref), zap.Error(err))
	}

	d.logger.Info("image removed", zap.String("ref", ref))

	return &daemonv1.RemoveImageResponse{}, nil
}

func (d *Daemon) DescribeImage(
	ctx context.Context, req *daemonv1.DescribeImageRequest,
) (*daemonv1.DescribeImageResponse, error) {
	ref := req.GetRef()

	// Cache-only: every ingest path (Build / Pull / Save / Push)
	// calls upsertImagesFromTags, which pre-warms the row's
	// describe blobs (config / labels / layers) via
	// cacheableDescribeBlobs. The RPC never falls back to a live
	// docker.Store ImageSave round trip — that would defeat the
	// point of the cache and add latency to every dashboard
	// describe-on-render. A cache miss returns NotFound so the
	// caller can decide (a) prompt the operator to pull, or (b)
	// trigger a RefreshImages sweep that re-populates the table.
	row, err := d.state.GetImage(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("describing %s: %w", ref, err)
	}

	if row == nil {
		return nil, fmt.Errorf("image %s: not in describe cache (try `otters image pull` or RefreshImages)", ref)
	}

	return d.persistedImageToDescribe(row), nil
}

// persistedImageToDescribe rebuilds a DescribeImageResponse from the
// cached row. Labels and layers are stored as JSON for compactness;
// decode failures fall back to empty values rather than blowing up
// the response — the cheap fields (ref / digest / artifactType)
// are always present and useful even when the JSON parse fails.
func (d *Daemon) persistedImageToDescribe(row *PersistedImage) *daemonv1.DescribeImageResponse {
	labels := map[string]string{}
	if row.LabelsJSON != "" {
		_ = json.Unmarshal([]byte(row.LabelsJSON), &labels)
	}

	var layers []string
	if row.LayersJSON != "" {
		_ = json.Unmarshal([]byte(row.LayersJSON), &layers)
	}

	return &daemonv1.DescribeImageResponse{
		Ref:          row.Ref,
		Digest:       row.Digest,
		ArtifactType: row.ArtifactType,
		Config:       row.ConfigJSON,
		Layers:       layers,
		Labels:       labels,
	}
}

func (d *Daemon) describeImageEmbedded(
	ctx context.Context, ref string,
) (*daemonv1.DescribeImageResponse, error) {
	addr := d.registry.Addr()

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

type manifestInfo struct {
	digest       string
	artifactType string
	size         int64
	// OCI standard annotations surfaced for the image listing UI:
	// description (free-text) and source (URL of the upstream repo,
	// rendered as a clickable link). Empty when the producer didn't
	// stamp the standard labels.
	description string
	source      string
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
			description:  pickLabel(index.Annotations, v1.AnnotationDescription, "description"),
			source:       pickLabel(index.Annotations, v1.AnnotationSource, "source"),
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
		description:  pickLabel(manifest.Annotations, v1.AnnotationDescription, "description"),
		source:       pickLabel(manifest.Annotations, v1.AnnotationSource, "source"),
	}, nil
}

// pickLabel returns the first non-empty value among the listed
// annotation keys. Lets us prefer the OCI standard
// `org.opencontainers.image.description` / `…image.source` labels
// while still surfacing values stamped with the bare `description` /
// `source` keys that hand-written Agentfiles (e.g. the meta-otter
// and the example scripts) tend to use.
func pickLabel(annotations map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := annotations[k]; v != "" {
			return v
		}
	}

	return ""
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

func splitRef(ref string) (string, string) {
	if idx := strings.LastIndex(ref, ":"); idx > 0 {
		return ref[:idx], ref[idx+1:]
	}

	return ref, "latest"
}
