// Server-component wrapper around the client view.
//
// `output: "export"` requires every dynamic segment to declare
// `generateStaticParams`. We return a single placeholder so Next has
// something to write — the daemon's SPA fallback (and `next dev`'s
// dynamic-route handling outside production) serves the React shell
// for any /images/<ref> URL, and `useRouteParams` picks up the real
// segment at mount.
import View from "./view"

export async function generateStaticParams(): Promise<{ ref: string }[]> {
	return [{ ref: "_" }]
}

export default function Page() {
	return <View />
}
