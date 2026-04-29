// Server-component wrapper around the client view.
//
// `output: "export"` requires every dynamic segment to declare
// `generateStaticParams`. We return a single placeholder so Next has
// something to write — the placeholder page is never directly visited
// because every transition into this route is a client-side navigation
// from /providers (which already has the agent name in hand). Daemon's
// SPA fallback also serves /index.html for any unknown URL, so a
// direct paste of /providers/<name>/edit gets the React shell and the
// router takes over on mount.
import View from "./view"

export async function generateStaticParams(): Promise<{ provider: string }[]> {
	return [{ provider: "_" }]
}

export default function Page() {
	return <View />
}
