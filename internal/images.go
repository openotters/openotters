package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	daemonv1 "github.com/openotters/cli/api/v1"
	"go.uber.org/zap"
)

func (d *Daemon) ListImages(
	_ context.Context, _ *daemonv1.ListImagesRequest,
) (*daemonv1.ListImagesResponse, error) {
	addr := d.registry.Addr()

	catalog, err := fetchJSON[catalogResponse](fmt.Sprintf("http://%s/v2/_catalog", addr))
	if err != nil {
		return nil, fmt.Errorf("listing repositories: %w", err)
	}

	var images []*daemonv1.ImageInfo

	for _, repo := range catalog.Repositories {
		tags, tagsErr := fetchJSON[tagsResponse](
			fmt.Sprintf("http://%s/v2/%s/tags/list", addr, repo),
		)
		if tagsErr != nil {
			continue
		}

		for _, tag := range tags.Tags {
			ref := repo + ":" + tag

			manifest, manifestErr := fetchManifestInfo(addr, repo, tag)
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
	_ context.Context, req *daemonv1.RemoveImageRequest,
) (*daemonv1.RemoveImageResponse, error) {
	addr := d.registry.Addr()
	ref := req.GetRef()

	repo, tag := splitRef(ref)

	manifest, err := fetchManifestInfo(addr, repo, tag)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", ref, err)
	}

	for _, deleteRef := range []string{tag, manifest.digest} {
		deleteURL := fmt.Sprintf("http://%s/v2/%s/manifests/%s", addr, repo, deleteRef)

		httpReq, reqErr := http.NewRequestWithContext(context.Background(), http.MethodDelete, deleteURL, nil)
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
	_ context.Context, req *daemonv1.DescribeImageRequest,
) (*daemonv1.DescribeImageResponse, error) {
	addr := d.registry.Addr()
	ref := req.GetRef()

	repo, tag := splitRef(ref)

	manifestData, digest, err := fetchManifestRaw(addr, repo, tag)
	if err != nil {
		return nil, fmt.Errorf("fetching manifest for %s: %w", ref, err)
	}

	var manifest v1.Manifest
	if err = json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}

	configData, err := fetchBlob(addr, repo, manifest.Config.Digest.String())
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

func fetchManifestInfo(addr, repo, tag string) (*manifestInfo, error) {
	data, digest, err := fetchManifestRaw(addr, repo, tag)
	if err != nil {
		return nil, err
	}

	var manifest v1.Manifest
	if parseErr := json.Unmarshal(data, &manifest); parseErr != nil {
		return nil, fmt.Errorf("parsing manifest: %w", parseErr)
	}

	return &manifestInfo{
		digest:       digest,
		artifactType: manifest.ArtifactType,
		size:         int64(len(data)),
	}, nil
}

func fetchManifestRaw(addr, repo, tag string) ([]byte, string, error) {
	url := fmt.Sprintf("http://%s/v2/%s/manifests/%s", addr, repo, tag)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
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

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	return data, resp.Header.Get("Docker-Content-Digest"), nil
}

func fetchBlob(addr, repo, digest string) ([]byte, error) {
	url := fmt.Sprintf("http://%s/v2/%s/blobs/%s", addr, repo, digest)

	resp, err := http.DefaultClient.Get(url) //nolint:noctx // internal registry call
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

func fetchJSON[T any](url string) (*T, error) {
	resp, err := http.DefaultClient.Get(url) //nolint:noctx // internal registry call
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
