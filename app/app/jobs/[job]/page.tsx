// Server-component wrapper around the client view.
//
// `output: "export"` requires every dynamic segment to declare
// `generateStaticParams`. We return a single placeholder so Next has
// something to write — every real visit happens via client-side
// navigation or the daemon's SPA fallback, which serves index.html
// for unknown paths and lets the React router resolve the URL.
import View from "./view"

export async function generateStaticParams(): Promise<{ job: string }[]> {
	return [{ job: "_" }]
}

export default function Page() {
	return <View />
}
