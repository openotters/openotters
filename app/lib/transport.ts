import type { Interceptor } from "@connectrpc/connect"
import { createConnectTransport } from "@connectrpc/connect-web"

// The Connect API is mounted at `/api/...` on the same listener that
// serves the embedded UI, so the production path is always
// same-origin: `baseUrl: "/api"` produces requests like
// `/api/openotters.daemon.v1.Runtime/GetInfo` which the browser
// considers same-host as the page that loaded it. No CORS preflight,
// no Allow-Origin headers needed.
//
// `NEXT_PUBLIC_OTTERSD_URL` overrides the origin (use the absolute
// daemon URL during `npm run dev` against a remote daemon, e.g.
// `http://127.0.0.1:5050`); the `/api` suffix is appended for you.
//
// Wire format defaults to Connect-JSON for DevTools-friendly payloads.
// Flip to `useBinaryFormat: true` (still Connect) or swap
// `createConnectTransport` → `createGrpcWebTransport` for gRPC-Web
// binary in production. The daemon's Connect-Go handler accepts all
// three protocols on the same path.
//
// JWT auth is handled SERVER-SIDE on the daemon's same listener: the
// `/api/*` mount auto-injects the operator token on requests arriving
// without an Authorization header. The browser never sees the token,
// so XSS / DevTools / extension snooping can't lift it. The dev UI
// mirrors this via app/middleware.ts injecting at the Next.js proxy.
// Dev: NEXT_PUBLIC_API_URL points the browser straight at the
// daemon, bypassing the Next.js dev rewrite (which buffers
// streaming responses and collapses ChatStreamWithAgent into one
// chunk at completion). The daemon's --allowed-origins lists
// localhost:3000 / 3001 / 3030 by default so CORS preflight
// resolves without intervention.
//
// Production: NEXT_PUBLIC_API_URL is unset (output: "export" keeps
// the embedded UI same-origin under the daemon's listener), so
// the transport uses the relative "/api" base.
//
// NEXT_PUBLIC_OTTERSD_URL is an older alias kept for backwards
// compatibility — explicit OTTERSD_URL=... in front of
// task ui:dev still works.
const origin =
	process.env.NEXT_PUBLIC_API_URL ?? process.env.NEXT_PUBLIC_OTTERSD_URL ?? ""

// Auth interceptor: attaches the dev bearer token to outgoing
// Connect requests directly from the browser. Previously we
// injected the header in app/middleware.ts at the Next.js proxy,
// but Edge Middleware that mutates request headers forces Next.js
// to buffer the upstream response — which breaks every
// server-streaming RPC (ChatStreamWithAgent ships its events in
// one batch at the end of the run). Doing it browser-side keeps
// the stream framed end-to-end.
//
// The token is read from `NEXT_PUBLIC_API_TOKEN`, baked into the
// client bundle by next.config.ts at dev start. Production builds
// embed the dashboard behind the daemon's own listener and never
// touch this code path.
const authInterceptor: Interceptor = (next) => async (req) => {
	const token = process.env.NEXT_PUBLIC_API_TOKEN
	if (token) {
		req.header.set("Authorization", `Bearer ${token}`)
	}
	return next(req)
}

export const transport = createConnectTransport({
	baseUrl: origin ? `${origin.replace(/\/$/, "")}/api` : "/api",
	useBinaryFormat: false,
	interceptors: [authInterceptor],
})
