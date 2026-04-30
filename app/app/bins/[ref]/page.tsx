// Server-component wrapper around the client view. See
// app/app/images/[ref]/page.tsx for the rationale on the
// `_` placeholder under `output: "export"`.
import View from "./view"

export async function generateStaticParams(): Promise<{ ref: string }[]> {
	return [{ ref: "_" }]
}

export default function Page() {
	return <View />
}
