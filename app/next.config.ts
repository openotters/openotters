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

// In dev, the browser talks to the daemon directly over CORS — no
// Next.js rewrite, no middleware. The previous proxy approach
// buffered chunked HTTP responses and collapsed every server-
// streaming RPC into one bulk delivery, so ChatStreamWithAgent
// looked frozen until the model finished. Going direct keeps the
// stream framed end-to-end.
//
// `API_URL` defaults to the dev daemon spawned by `task daemon:dev`
// (127.0.0.1:5050). Override with API_URL=http://127.0.0.1:5500 to
// point the dev UI at the brew daemon. Daemon-side
// --allowed-origins covers localhost:3000 / 3001 / 3030 out of the
// box so CORS preflight resolves without intervention.
//
// Production builds run behind the daemon's own listener
// (`output: "export"`), so NEXT_PUBLIC_API_URL is left unset and the
// Connect transport falls back to a relative `/api` base.
const env: Record<string, string> = {}
if (!isProd) {
	env.NEXT_PUBLIC_API_URL = process.env.API_URL ?? "http://127.0.0.1:5050"
	if (process.env.API_TOKEN) {
		env.NEXT_PUBLIC_API_TOKEN = process.env.API_TOKEN
	}
}

const nextConfig: NextConfig = {
	env,
	output: isProd ? "export" : undefined,
	trailingSlash: true,
	reactStrictMode: true,
	poweredByHeader: false,
	images: {
		unoptimized: true,
	},
}

export default nextConfig
