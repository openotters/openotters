package internal

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"charm.land/catwalk/pkg/catwalk"
)

// catwalkDefaultURL points at Charm's hosted Catwalk service. The
// `catwalk.New()` client defaults to localhost:8080 (for folks
// running a private Catwalk), so we override unless the operator
// explicitly set OTTERS_CATWALK_URL.
const catwalkDefaultURL = "https://catwalk.charm.sh"

// catwalkFetchTimeout caps how long a single GetProviders round-trip
// can take. Catwalk is served by Charm's metadata host; a slow
// network shouldn't block `otters models ls` indefinitely.
const catwalkFetchTimeout = 5 * time.Second

// catwalkCacheTTL bounds how often we'll ask Charm's Catwalk service
// for its provider database. The payload is ~200 KB of curated
// metadata that changes on the order of days, so serving from an
// in-memory cache for five minutes is a safe responsiveness win.
const catwalkCacheTTL = 5 * time.Minute

// catwalkCatalogue wraps the Catwalk client with a lock-guarded
// in-memory cache. Exported as a Daemon field rather than a package
// singleton so test doubles can inject a stub or a different URL.
type catwalkCatalogue struct {
	client *catwalk.Client

	mu        sync.Mutex
	providers []catwalk.Provider
	fetchedAt time.Time
}

func newCatwalkCatalogue() *catwalkCatalogue {
	url := os.Getenv("OTTERS_CATWALK_URL")
	if url == "" {
		url = catwalkDefaultURL
	}

	return &catwalkCatalogue{client: catwalk.NewWithURL(url)}
}

// providers returns the cached provider list, refreshing it from
// Catwalk when stale. Errors are propagated to the caller so the
// daemon can log + fall back to the "<provider>/*" placeholder.
func (c *catwalkCatalogue) fetchProviders(ctx context.Context) ([]catwalk.Provider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.providers != nil && time.Since(c.fetchedAt) < catwalkCacheTTL {
		return c.providers, nil
	}

	fetchCtx, cancel := context.WithTimeout(ctx, catwalkFetchTimeout)
	defer cancel()

	list, err := c.client.GetProviders(fetchCtx, "")
	if err != nil {
		return nil, fmt.Errorf("catwalk: %w", err)
	}

	c.providers = list
	c.fetchedAt = time.Now()

	return list, nil
}

// modelsFor returns every model Catwalk lists for the given provider
// name (matched against Catwalk's InferenceProvider ID). Empty when
// the name isn't in Catwalk's database — callers should fall back
// gracefully for user-defined / self-hosted providers.
func (c *catwalkCatalogue) modelsFor(ctx context.Context, name string) ([]catwalk.Model, error) {
	providers, err := c.fetchProviders(ctx)
	if err != nil {
		return nil, err
	}

	for _, p := range providers {
		if string(p.ID) == name {
			return p.Models, nil
		}
	}

	return nil, nil
}
