"use client"

import { ArtifactDetailView } from "@/components/artifact-detail-view"
import { useRouteParams } from "@/lib/use-route-params"

export default function BinDetailPage() {
	const params = useRouteParams<{ ref: string }>("/bins/:ref")
	const ref = params.ref ?? ""

	return <ArtifactDetailView kind="bin" ref={ref} />
}
