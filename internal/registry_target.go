package internal

import (
	"context"
	"io"
	"strings"

	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry/remote"

	agentoci "github.com/openotters/agentfile/oci"
	"github.com/openotters/agentfile/spec"
)

// registryTarget adapts a single-repo remote.Repository to the
// oras.ReadOnlyTarget contract afstore.Load expects. The adapter is
// only needed because afstore.Load calls s.Resolve(ctx, ref.String())
// with the *full* ref (e.g. "127.0.0.1:12345/reader:latest") while
// remote.Repository.Resolve only accepts a bare tag or digest.
//
// Fetch and Exists pass through unchanged — they're already content-
// addressed and don't care about the ref string.
type registryTarget struct {
	repo *remote.Repository
	name string // the ref's Name portion, e.g. "127.0.0.1:12345/reader"
}

var _ oras.ReadOnlyTarget = (*registryTarget)(nil)

func newRegistryTarget(ref spec.Reference) (*registryTarget, error) {
	repo, err := agentoci.NewRemoteRepository(ref, agentoci.WithPlainHTTP)
	if err != nil {
		return nil, err
	}

	return &registryTarget{repo: repo, name: ref.Name}, nil
}

func (t *registryTarget) Resolve(ctx context.Context, reference string) (v1.Descriptor, error) {
	return t.repo.Resolve(ctx, shortenRef(reference, t.name))
}

func (t *registryTarget) Fetch(ctx context.Context, target v1.Descriptor) (io.ReadCloser, error) {
	return t.repo.Fetch(ctx, target)
}

func (t *registryTarget) Exists(ctx context.Context, target v1.Descriptor) (bool, error) {
	return t.repo.Exists(ctx, target)
}

// erroringTarget is the sentinel returned by the storeFor factory when
// constructing a real target fails (e.g. a malformed persisted ref). It
// implements oras.ReadOnlyTarget by returning the captured error on
// every call, so agent materialization surfaces the root cause through
// normal error paths instead of panicking on a nil store.
type erroringTarget struct {
	err error
}

var _ oras.ReadOnlyTarget = erroringTarget{}

func (t erroringTarget) Resolve(context.Context, string) (v1.Descriptor, error) {
	return v1.Descriptor{}, t.err
}

func (t erroringTarget) Fetch(context.Context, v1.Descriptor) (io.ReadCloser, error) {
	return nil, t.err
}

func (t erroringTarget) Exists(context.Context, v1.Descriptor) (bool, error) {
	return false, t.err
}

// shortenRef reduces a full reference like "127.0.0.1:12345/reader:latest"
// or "127.0.0.1:12345/reader@sha256:..." down to what remote.Repository
// expects: the tag ("latest") or the digest ("sha256:..."). If the input
// doesn't carry the target's Name prefix, it's assumed to already be a
// tag/digest and returned as-is.
func shortenRef(reference, name string) string {
	if rest, ok := strings.CutPrefix(reference, name+":"); ok {
		return rest
	}

	if rest, ok := strings.CutPrefix(reference, name+"@"); ok {
		return rest
	}

	return reference
}
