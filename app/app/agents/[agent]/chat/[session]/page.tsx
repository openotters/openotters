import View from "./view"

export async function generateStaticParams(): Promise<
	{ agent: string; session: string }[]
> {
	return [{ agent: "_", session: "_" }]
}

export default function Page() {
	return <View />
}
