import View from "./view"

export async function generateStaticParams(): Promise<{ agent: string }[]> {
	return [{ agent: "_" }]
}

export default function Page() {
	return <View />
}
