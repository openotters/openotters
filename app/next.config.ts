import type { NextConfig } from "next"

// `output: "export"` is the build mode that gets us a static `out/`
// directory the Go daemon embeds via `go:embed`. Trade-offs:
//
//   - No Server Components rendering on request — every page in this
//     app is `"use client"` so this is a no-op.
//   - Dynamic routes (e.g. /agents/[agent]) need `generateStaticParams`;
//     we return a single `_` placeholder. In production, the daemon's
//     SPA fallback (internal/webui/webui.go) maps live URLs like
//     /agents/brave-panda/chat onto the placeholder bundle and
//     `useRouteParams` reads the real segment from window.location.
//   - `images.unoptimized: true` is required (next/image's default
//     loader needs a runtime).
//
// `trailingSlash: true` makes Next emit `/agents/index.html` rather
// than `/agents.html`, which the standard http.FileServer in the
// daemon resolves out of the box.
//
// `output: "export"` is only set in production builds — `next dev`
// (run via `task ui:dev`) doesn't ship to disk, and the dev server
// rejects arbitrary param values when export is enabled, breaking
// direct visits to `/agents/<real-name>/chat`. Letting dev mode run
// without `output: "export"` makes those URLs work like a normal
// Next.js dev session; the production build path is unchanged.
const isProd = process.env.NODE_ENV === "production"

// In dev, proxy `/api/*` to the daemon so the browser sees the API
// as same-origin — no CORS preflight, no allowlist juggling when the
// dev server falls through to :3001 because :3000 is busy.
//
// `API_URL` defaults to the dev daemon spawned by `task daemon:dev`
// (127.0.0.1:5050). Override it to point at any other ottersd
// instance, e.g. `API_URL=http://127.0.0.1:5500 task ui:dev` to talk
// to the brew daemon. Only consulted in dev — production builds are
// `output: "export"` and live behind the daemon's own listener.
const apiURL = process.env.API_URL ?? "http://127.0.0.1:5050"

const nextConfig: NextConfig = {
	output: isProd ? "export" : undefined,
	trailingSlash: true,
	// Pages still resolve to `/foo/index.html` (we keep `trailingSlash`
	// for the static export), but the dev server stops 308'ing requests
	// to add a trailing slash. Required for the `/api/*` rewrite below
	// — Connect RPC paths like `/api/.../GetInfo` must not be rewritten
	// to `.../GetInfo/`, which the daemon's mux doesn't match.
	skipTrailingSlashRedirect: true,
	reactStrictMode: true,
	poweredByHeader: false,
	images: {
		unoptimized: true,
	},
	async rewrites() {
		if (isProd) return []
		return [
			{
				source: "/api/:path*",
				destination: `${apiURL.replace(/\/$/, "")}/api/:path*`,
			},
		]
	},
}

export default nextConfig
