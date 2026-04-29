import type { NextConfig } from "next"

// `output: "export"` is the build mode that gets us a static `out/`
// directory the Go daemon embeds via `go:embed`. Trade-offs:
//
//   - No Server Components rendering on request — every page in this
//     app is `"use client"` so this is a no-op.
//   - Dynamic routes (e.g. /agents/[agent]) need `generateStaticParams`;
//     we return an empty array there, relying on SPA-style fallback on
//     the daemon's file server (any unknown path serves index.html and
//     the React router takes over).
//   - `images.unoptimized: true` is required (next/image's default
//     loader needs a runtime).
//
// `trailingSlash: true` makes Next emit `/agents/index.html` rather
// than `/agents.html`, which the standard http.FileServer in the
// daemon resolves out of the box.
const nextConfig: NextConfig = {
	output: "export",
	trailingSlash: true,
	reactStrictMode: true,
	poweredByHeader: false,
	images: {
		unoptimized: true,
	},
}

export default nextConfig
