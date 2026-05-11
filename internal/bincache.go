package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"

	agentoci "github.com/openotters/agentfile/oci"
	"github.com/openotters/agentfile/spec"
)

// newCachingBinPuller returns a Puller that serves BIN images from the
// embedded registry at embeddedAddr, mirroring from the remote on first
// miss. Cached entries preserve the upstream repository path verbatim
// under the embedded registry, so the same upstream ref always resolves
// to the same local manifest across daemon restarts — subsequent runs
// are offline-capable. Bin vs agent separation is carried by
// artifactType on the manifest, not by path prefix.
//
// If the ref already points at the embedded registry (e.g. an agentfile
// that hardcodes 127.0.0.1:<port>/...), we skip mirroring and fetch
// directly — callers opted into the local path explicitly.
func newCachingBinPuller(embeddedAddr string) agentoci.Puller {
	return func(ctx context.Context, ref spec.Reference, w io.Writer) error {
		if embeddedAddr == "" {
			return agentoci.RemotePuller()(ctx, ref, w)
		}

		if strings.HasPrefix(ref.Name, embeddedAddr) {
			return agentoci.RemotePuller(agentoci.WithPlainHTTP)(ctx, ref, w)
		}

		local := localBinRef(embeddedAddr, ref)

		if !existsInEmbedded(ctx, local) {
			if err := mirrorBinImage(ctx, ref, local); err != nil {
				return fmt.Errorf("mirroring %s: %w", ref, err)
			}
		}

		return agentoci.RemotePuller(agentoci.WithPlainHTTP)(ctx, local, w)
	}
}

// newCachingUsageFetcher mirrors newCachingBinPuller for the USAGE.md
// metadata layer. Bins materialise their docs at the same time the
// binary is installed, so on the cold-start path the doc fetch races
// the binary fetch. Both paths use the same mirror-then-read
// pattern: mirror the entire upstream image into the embedded
// registry on first miss, then serve every subsequent request
// (binary or docs) from the local copy. Whichever request lands
// first triggers the mirror; the other reads through.
func newCachingUsageFetcher(embeddedAddr string) agentoci.UsageFetcher {
	return func(ctx context.Context, ref spec.Reference) (string, error) {
		if embeddedAddr == "" {
			return agentoci.RemoteUsageFetcher()(ctx, ref)
		}

		if strings.HasPrefix(ref.Name, embeddedAddr) {
			return agentoci.RemoteUsageFetcher(agentoci.WithPlainHTTP)(ctx, ref)
		}

		local := localBinRef(embeddedAddr, ref)

		if !existsInEmbedded(ctx, local) {
			if err := mirrorBinImage(ctx, ref, local); err != nil {
				return "", fmt.Errorf("mirroring %s: %w", ref, err)
			}
		}

		return agentoci.RemoteUsageFetcher(agentoci.WithPlainHTTP)(ctx, local)
	}
}

// localBinRef maps an upstream reference to its embedded-registry
// counterpart by prepending the embedded registry's address. The
// upstream repository path (including any dots in the hostname) is
// valid under the OCI distribution grammar and passed through
// unchanged — that gives an injective, readable mapping.
func localBinRef(embeddedAddr string, upstream spec.Reference) spec.Reference {
	return spec.Reference{
		Name: embeddedAddr + "/" + upstream.Name,
		Tag:  upstream.Tag,
	}
}

func existsInEmbedded(ctx context.Context, ref spec.Reference) bool {
	repo, err := agentoci.NewRemoteRepository(ref, agentoci.WithPlainHTTP)
	if err != nil {
		return false
	}

	tag := ref.Tag
	if tag == "" {
		tag = spec.DefaultTag
	}

	_, err = repo.Resolve(ctx, tag)

	return err == nil
}

func mirrorBinImage(ctx context.Context, upstream, local spec.Reference) error {
	src, err := agentoci.NewRemoteRepository(upstream)
	if err != nil {
		return fmt.Errorf("opening upstream %s: %w", upstream, err)
	}

	dst, err := agentoci.NewRemoteRepository(local, agentoci.WithPlainHTTP)
	if err != nil {
		return fmt.Errorf("opening local %s: %w", local, err)
	}

	srcTag := upstream.Tag
	if srcTag == "" {
		srcTag = spec.DefaultTag
	}

	dstTag := local.Tag
	if dstTag == "" {
		dstTag = spec.DefaultTag
	}

	_, err = oras.Copy(ctx, src, srcTag, dst, dstTag, copyOptions())

	return err
}

// copyOptions returns oras copy options with the fallback FindSuccessors
// wired up. Shared by the caching puller and all daemon pull/push paths
// so every mirror goes through the same graph-walking defense.
func copyOptions() oras.CopyOptions {
	opts := oras.DefaultCopyOptions
	opts.FindSuccessors = successorsWithFallback

	return opts
}

// successorsWithFallback extends oras' standard MediaType-dispatched
// successor resolution with a JSON-body fallback for descriptors whose
// MediaType oras doesn't recognize (e.g. an index served by a registry
// that appends a charset parameter, or a custom artifactType whose
// MediaType happens to be unknown to this version of oras-go). Without
// this, oras pushes the root manifest without its children and the
// destination rejects the index because its sub-manifests are missing.
func successorsWithFallback(
	ctx context.Context, fetcher content.Fetcher, desc v1.Descriptor,
) ([]v1.Descriptor, error) {
	successors, err := content.Successors(ctx, fetcher, desc)
	if err != nil || len(successors) > 0 {
		return successors, err
	}

	if !strings.Contains(desc.MediaType, "json") &&
		!strings.Contains(desc.MediaType, "manifest") &&
		!strings.Contains(desc.MediaType, "index") {
		return nil, nil
	}

	body, fetchErr := content.FetchAll(ctx, fetcher, desc)
	if fetchErr != nil {
		// Fallback path: a fetch failure here means oras' default
		// dispatch couldn't resolve the descriptor either. Returning the
		// error would abort the entire copy — instead, treat the
		// descriptor as having no successors so the parent commits and
		// the registry rejects it cleanly with a clearer message.
		return nil, nil //nolint:nilerr // best-effort fallback; see comment
	}

	var probe struct {
		Manifests []v1.Descriptor `json:"manifests"`
		Subject   *v1.Descriptor  `json:"subject"`
		Config    v1.Descriptor   `json:"config"`
		Layers    []v1.Descriptor `json:"layers"`
	}

	if json.Unmarshal(body, &probe) != nil {
		// Body isn't a manifest/index JSON — no successors to walk.
		return nil, nil //nolint:nilerr // best-effort fallback; see comment
	}

	if len(probe.Manifests) > 0 {
		nodes := probe.Manifests
		if probe.Subject != nil {
			nodes = append([]v1.Descriptor{*probe.Subject}, nodes...)
		}

		return nodes, nil
	}

	if probe.Config.Digest != "" {
		nodes := []v1.Descriptor{probe.Config}
		if probe.Subject != nil {
			nodes = append([]v1.Descriptor{*probe.Subject}, nodes...)
		}

		return append(nodes, probe.Layers...), nil
	}

	return nil, nil
}
