"use client"

import { useQuery } from "@connectrpc/connect-query"
import { Plus } from "lucide-react"
import Link from "next/link"
import { useMemo } from "react"
import { AgentCard } from "@/components/agent-card"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { listAgents } from "@/lib/proto/v1/daemon-Runtime_connectquery"

export default function AgentsPage() {
	const { data, isLoading, error } = useQuery(listAgents, {})
	const agents = data?.agents ?? []

	const sorted = useMemo(
		() => [...agents].sort((a, b) => a.name.localeCompare(b.name)),
		[agents],
	)

	return (
		<div className="space-y-6">
			<PageHeader
				actions={
					<Button asChild>
						<Link href="/agents/new">
							<Plus className="mr-2 h-4 w-4" />
							Create Agent
						</Link>
					</Button>
				}
				command="otters ps"
				description="Running and stopped agents managed by ottersd."
				title="Agents"
			/>

			{error && (
				<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
					Failed to reach daemon: {error.message}. Make sure{" "}
					<code className="font-mono">ottersd serve --http 127.0.0.1:5500</code> is running.
				</div>
			)}

			{isLoading && <p className="text-muted-foreground">Loading agents…</p>}

			{!isLoading && !error && sorted.length > 0 && (
				<div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
					{sorted.map((agent) => (
						<AgentCard agent={agent} key={agent.id} />
					))}
				</div>
			)}

			{!isLoading && !error && sorted.length === 0 && (
				<div className="flex flex-col items-center justify-center rounded-lg border border-dashed py-12">
					<p className="mb-4 text-muted-foreground">No agents yet. Create your first agent to get started.</p>
					<Button asChild>
						<Link href="/agents/new">
							<Plus className="mr-2 h-4 w-4" />
							Create Agent
						</Link>
					</Button>
				</div>
			)}
		</div>
	)
}
