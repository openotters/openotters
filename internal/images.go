package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
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

// RefreshImages reconciles the daemon's images cache with the
// executor registry's authoritative state. Walks ListEntries,
// asks ManifestKind for each (or carries forward what the existing
// cache row knew), and replaces the table in one transaction.
// Returns the count of images currently in the cache after
// refresh — useful for UI confirmation.
func (d *Daemon) RefreshImages(
	ctx context.Context, _ *daemonv1.RefreshImagesRequest,
) (*daemonv1.RefreshImagesResponse, error) {
	reg := d.pool.provider.Registry()

	entries, err := reg.ListEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing images: %w", err)
	}

	existing, err := d.state.ListImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading existing image cache: %w", err)
	}

	// Carry forward known artifactType per digest from the
	// existing cache so a refresh doesn't blank kinds the docker
	// executor can't re-derive (its ManifestKind always returns
	// empty). A fresh build / pull will overwrite any stale value
	// anyway.
	priorKindByDigest := make(map[string]string, len(existing))

	for _, p := range existing {
		if p.ArtifactType != "" && priorKindByDigest[p.Digest] == "" {
			priorKindByDigest[p.Digest] = p.ArtifactType
		}
	}

	out := make([]PersistedImage, 0, len(entries))

	for _, e := range entries {
		kind, kindErr := reg.ManifestKind(ctx, e.Ref)
		if kindErr != nil {
			d.logger.Debug("manifest kind for refresh",
				zap.String("ref", e.Ref), zap.Error(kindErr))
		}

		if kind == "" {
			kind = priorKindByDigest[e.Digest]
		}

		out = append(out, PersistedImage{
			Ref:          e.Ref,
			Digest:       e.Digest,
			ArtifactType: kind,
			Size:         e.Size,
			CreatedUnix:  e.CreatedUnix,
			Description:  e.Description,
			Source:       e.Source,
		})
	}

	if err = d.state.ReplaceAllImages(ctx, out); err != nil {
		return nil, fmt.Errorf("replacing images cache: %w", err)
	}

	d.logger.Info("images refreshed", zap.Int("count", len(out)))

	count := len(out)
	if count > math.MaxInt32 {
		count = math.MaxInt32
	}

	return &daemonv1.RefreshImagesResponse{Count: int32(count)}, nil
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

	// System executor exposes an embedded HTTP OCI registry that lets
	// us walk manifest + config + layer descriptors directly. Docker
	// executor doesn't (the daemon's image API is the only window
	// onto the store), so we fall back to executor.Registry.Inspect
	// for the metadata fields and skip the raw config / layer blobs.
	// Inspect returns enough for the dashboard's image-detail card;
	// the env card (which parses the config blob) gracefully degrades
	// when Config is empty.
	if d.registry != nil {
		return d.describeImageEmbedded(ctx, ref)
	}

	info, err := d.pool.provider.Registry().Inspect(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("describing %s: %w", ref, err)
	}

	return &daemonv1.DescribeImageResponse{
		Ref:          info.Ref,
		Digest:       info.Digest,
		ArtifactType: info.MediaType,
		Labels:       info.Annotations,
	}, nil
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
