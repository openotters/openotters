// Package webui owns the daemon's embedded web UI handler. The Next.js
// build artefact (a static export) lives under dist/. The Taskfile
// target `task ui:build` populates it from the sibling openotters-app
// repo; a placeholder lives in `dist/.gitkeep` so `go:embed` succeeds
// on a fresh checkout that hasn't run the build yet.
//
// Two serving modes are supported:
//
//   - Embedded (default): files baked into the binary at compile time.
//     `--ui-path` unset, `Handler("")`. Self-contained binary.
//
//   - Disk override: serve from a directory passed via `--ui-path`.
//     Useful for running a custom build (e.g. a local fork) without
//     rebuilding ottersd. `Handler("/path/to/out")`.
//
// Both modes apply SPA-style fallback: missing routes resolve to
// `index.html` so client-side routing works on direct URL visits
// (e.g. someone pasting `/agents/foo` into the address bar).
package webui

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

//go:embed all:dist
var embedded embed.FS

// Handler returns an http.Handler serving the web UI. When diskPath is
// empty, the embedded build is used; otherwise files are read from
// disk so `task ui:build` (or a developer's `next build`) doesn't
// require recompiling ottersd.
func Handler(diskPath string) http.Handler {
	if diskPath != "" {
		return spaHandler(http.Dir(diskPath))
	}

	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return notBuiltHandler()
	}

	return spaHandler(http.FS(sub))
}

// dynamicRoute is a Next.js dynamic-route placeholder discovered in the
// static export (any `index.html` whose path contains a `_` segment).
// We use the placeholder's path to derive a regex that matches live
// URLs at the same shape, so visiting `/agents/brave-panda/chat`
// serves the `/agents/_/chat/index.html` bundle while keeping the
// browser URL unchanged — `useParams()` then reads the real value.
type dynamicRoute struct {
	re    *regexp.Regexp
	bytes []byte
}

// spaHandler serves a Next.js static export with Single-Page-App
// semantics:
//
//  1. Exact file hit → serve it directly.
//  2. `<path>/index.html` exists (Next emits these with
//     `trailingSlash: true`) → serve via FileServer so directory
//     redirects/canonicalisation work.
//  3. `<path>.html` exists → serve the .html sibling.
//  4. `_next/`, `static/`, `assets/` paths → real 404; never mask a
//     missing JS bundle with the SPA shell.
//  5. Dynamic-route placeholder (`/agents/_/chat/index.html` etc.)
//     matches the URL shape → serve those bytes directly under the
//     URL the user typed, so React's `useParams()` sees the real
//     param value.
//  6. Otherwise → serve `/index.html` so the React router can take
//     over.
//
// We bypass `http.FileServer` for the index.html / placeholder
// fallbacks. The stdlib's file server insists on redirecting
// `/index.html` to `./` for canonicalisation, which would loop back to
// this handler and re-trigger the fallback indefinitely. Reading the
// bytes ourselves and calling `http.ServeContent` keeps the URL the
// user typed and lets the browser cache it normally.
func spaHandler(root http.FileSystem) http.HandlerFunc {
	server := http.FileServer(root)

	// Snapshot index.html and dynamic-route placeholders at
	// handler-construction time. The embedded FS never changes; for
	// disk mode we accept slightly stale bytes rather than re-read on
	// every miss. Restart the daemon (or use --ui-path with a fresh
	// build) to refresh.
	indexBytes, indexLoaded := readIndex(root)
	routes := buildDynamicRoutes(root)

	return func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean(r.URL.Path)
		if clean == "" || clean == "." {
			clean = "/"
		}

		// Rule 1: exact file hit.
		if isFile(root, clean) {
			server.ServeHTTP(w, r)
			return
		}

		// Rule 2: directory with an index.html (static route under
		// trailingSlash: true). Delegate to FileServer so a
		// no-trailing-slash visit gets the canonical redirect.
		if isFile(root, path.Join(clean, "index.html")) {
			server.ServeHTTP(w, r)
			return
		}

		// Rule 3: try `<path>.html` for extensionless URLs without a
		// trailing slash.
		if !strings.Contains(path.Base(clean), ".") && !strings.HasSuffix(r.URL.Path, "/") {
			if isFile(root, clean+".html") {
				r2 := r.Clone(r.Context())
				r2.URL.Path = clean + ".html"
				server.ServeHTTP(w, r2)
				return
			}
		}

		// Rule 4: asset paths are real 404s.
		if strings.HasPrefix(clean, "/_next/") ||
			strings.HasPrefix(clean, "/static/") ||
			strings.HasPrefix(clean, "/assets/") {
			http.NotFound(w, r)
			return
		}

		// Rule 5: dynamic-route placeholder match.
		for _, rt := range routes {
			if rt.re.MatchString(clean) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(rt.bytes))

				return
			}
		}

		// Rule 6: SPA fallback — serve index.html bytes directly.
		if !indexLoaded {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(indexBytes))
	}
}

// isFile reports whether p resolves to a regular file inside root.
// Directories don't count — http.FileServer's directory-listing /
// canonical-redirect logic would fight with the SPA fallback.
func isFile(root http.FileSystem, p string) bool {
	f, err := root.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return false
	}

	return !stat.IsDir()
}

func readIndex(root http.FileSystem) ([]byte, bool) {
	f, err := root.Open("/index.html")
	if err != nil {
		return nil, false
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false
	}

	return data, true
}

// buildDynamicRoutes walks the FS looking for `index.html` files whose
// path contains one or more `_` segments — these are Next.js dynamic
// routes that we exported with `generateStaticParams()` returning a
// single `_` placeholder. Each becomes a regex (the `_` segment turns
// into `[^/]+`) so a live URL like `/agents/brave-panda/chat` resolves
// to the `/agents/_/chat/index.html` bundle without redirecting.
func buildDynamicRoutes(root http.FileSystem) []dynamicRoute {
	var routes []dynamicRoute

	walkFS(root, "/", func(p string) {
		if !strings.HasSuffix(p, "/index.html") {
			return
		}

		dir := strings.TrimSuffix(p, "/index.html")
		segments := strings.Split(dir, "/")

		hasPlaceholder := false

		for _, s := range segments {
			if s == "_" {
				hasPlaceholder = true
				break
			}
		}

		if !hasPlaceholder {
			return
		}

		rebuilt := make([]string, 0, len(segments))

		for _, s := range segments {
			if s == "_" {
				rebuilt = append(rebuilt, "[^/]+")
				continue
			}

			rebuilt = append(rebuilt, regexp.QuoteMeta(s))
		}

		pattern := "^" + strings.Join(rebuilt, "/") + "/?$"

		re, err := regexp.Compile(pattern)
		if err != nil {
			return
		}

		f, err := root.Open(p)
		if err != nil {
			return
		}

		data, readErr := io.ReadAll(f)
		_ = f.Close()

		if readErr != nil {
			return
		}

		routes = append(routes, dynamicRoute{re: re, bytes: data})
	})

	return routes
}

// walkFS is a minimal recursive walk over an http.FileSystem. We can't
// use io/fs.WalkDir directly because spaHandler accepts the wider
// http.FileSystem interface (so disk mode and embedded mode share a
// code path).
func walkFS(root http.FileSystem, dir string, fn func(string)) {
	f, err := root.Open(dir)
	if err != nil {
		return
	}

	entries, err := f.Readdir(-1)
	_ = f.Close()

	if err != nil {
		return
	}

	for _, e := range entries {
		full := path.Join(dir, e.Name())
		if !strings.HasPrefix(full, "/") {
			full = "/" + full
		}

		if e.IsDir() {
			walkFS(root, full, fn)

			continue
		}

		fn(full)
	}
}

// notBuiltHandler returns a tiny page when the embedded UI is empty
// (e.g. fresh checkout, no build yet). The Connect API still works on
// this listener; this kicks in only for `/` and any UI route.
func notBuiltHandler() http.Handler {
	const body = `<!doctype html><html><head><title>UI not built</title>` +
		`<style>body{font-family:system-ui,sans-serif;padding:3rem;color:#222;background:#fafafa}` +
		`code{background:#eee;padding:.1em .3em;border-radius:.2em}</style></head>` +
		`<body><h1>OpenOtters daemon is up</h1>` +
		`<p>The web UI hasn't been built into this binary. Run <code>task ui:build</code> ` +
		`from the daemon repo (or pass <code>--ui-path</code> to serve from disk), then restart ottersd.</p>` +
		`<p>The Connect/gRPC API is reachable on this listener — try ` +
		`<code>curl -d '{}' -H 'Content-Type: application/json' /openotters.daemon.v1.Runtime/GetInfo</code>.</p>` +
		`</body></html>`

	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	})
}
