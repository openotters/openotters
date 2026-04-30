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

const nextConfig: NextConfig = {
	output: isProd ? "export" : undefined,
	trailingSlash: true,
	reactStrictMode: true,
	poweredByHeader: false,
	images: {
		unoptimized: true,
	},
}

export default nextConfig
