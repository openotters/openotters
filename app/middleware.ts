// Edge-runtime middleware that injects the operator JWT into every
// proxied /api/* request during dev. Production builds run behind
// the daemon's own listener, where the same auth is enforced
// directly — middleware is dev-only.
//
// The token is read from $API_TOKEN at build/start time. Operators
// run dev with:
//
//   API_TOKEN=$(jq -r '.endpoints["unix:///tmp/otters-dev.sock"].token' \
//                  ~/.otters/credentials.json) task ui:dev
//
// (or wire $API_TOKEN into their shell profile). Without it, the
// browser sees Unauthenticated from every API call — fail-closed,
// no silent half-loaded state.
//
// The rewrite still happens in next.config.ts (URL → upstream); this
// middleware just layers a header on top. Connect-Go on the daemon
// side accepts gRPC-Web / Connect / gRPC over HTTP/1 + HTTP/2 with
// the same Bearer-token interceptor.

import { type NextRequest, NextResponse } from "next/server"

export const config = {
	// Match the API surface only. Static assets / page routes never
	// need the bearer header — they fetch from the same Next server.
	matcher: "/api/:path*",
}

export function middleware(req: NextRequest) {
	const token = process.env.API_TOKEN
	if (!token) {
		// No token configured — let the request through unmodified
		// so the daemon's own Unauthenticated response surfaces in
		// the browser. The error message points at credentials.json,
		// which is the right diagnostic.
		return NextResponse.next()
	}

	const headers = new Headers(req.headers)
	headers.set("Authorization", `Bearer ${token}`)
	return NextResponse.next({ request: { headers } })
}
