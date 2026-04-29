"use client"

import { useQuery } from "@connectrpc/connect-query"
import { Bot, Layers, Plug, Terminal } from "lucide-react"
import Link from "next/link"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { listAgents, listImages, listProviders } from "@/lib/proto/v1/daemon-Runtime_connectquery"

interface StatCardProps {
	href: string
	title: string
	value: number
	description: string
	icon: React.ReactNode
	loading?: boolean
}

// Each stat card is now a Link — clicking the count drops you straight
// into the matching list page. The whole card is the hit target so a
// click anywhere counts. transition-colors + hover state make the
// affordance obvious; the underlying Card preserves its border.
function StatCard({ href, title, value, description, icon, loading }: StatCardProps) {
	return (
		<Link className="block transition-colors hover:bg-muted/50 rounded-xl" href={href}>
			<Card className="h-full">
				<CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
					<CardTitle className="font-medium text-sm">{title}</CardTitle>
					<div className="text-muted-foreground">{icon}</div>
				</CardHeader>
				<CardContent>
					<div className="font-bold text-2xl">{loading ? "—" : value}</div>
					<p className="text-muted-foreground text-xs">{description}</p>
				</CardContent>
			</Card>
		</Link>
	)
}

const BIN_ARTIFACT_TYPE = "application/vnd.openotters.bin.v1"

export function ResourceStats() {
	const agents = useQuery(listAgents, {})
	const images = useQuery(listImages, {})
	const providers = useQuery(listProviders, {})

	const allAgents = agents.data?.agents ?? []
	const runningAgents = allAgents.filter((a) => a.status === "running").length
	const allImages = images.data?.images ?? []
	const agentImages = allImages.filter((i) => i.artifactType !== BIN_ARTIFACT_TYPE)
	const binImages = allImages.filter((i) => i.artifactType === BIN_ARTIFACT_TYPE)
	const allProviders = providers.data?.providers ?? []

	return (
		<div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
			<StatCard
				description={`${runningAgents} running`}
				href="/agents"
				icon={<Bot className="h-4 w-4" />}
				loading={agents.isLoading}
				title="Agents"
				value={allAgents.length}
			/>
			<StatCard
				description="in registry"
				href="/images"
				icon={<Layers className="h-4 w-4" />}
				loading={images.isLoading}
				title="Images"
				value={agentImages.length}
			/>
			<StatCard
				description="configured"
				href="/providers"
				icon={<Plug className="h-4 w-4" />}
				loading={providers.isLoading}
				title="Providers"
				value={allProviders.length}
			/>
			<StatCard
				description="in registry"
				href="/bins"
				icon={<Terminal className="h-4 w-4" />}
				loading={images.isLoading}
				title="Bins"
				value={binImages.length}
			/>
		</div>
	)
}
