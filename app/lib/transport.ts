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
const origin = process.env.NEXT_PUBLIC_OTTERSD_URL ?? ""

export const transport = createConnectTransport({
	baseUrl: origin ? `${origin.replace(/\/$/, "")}/api` : "/api",
	useBinaryFormat: false,
})
