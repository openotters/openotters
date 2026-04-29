"use client"

import { useQuery } from "@connectrpc/connect-query"
import { Plus } from "lucide-react"
import Link from "next/link"
import { AgentCard } from "@/components/agent-card"
import { Button } from "@/components/ui/button"
import { listAgents } from "@/lib/proto/v1/daemon-Runtime_connectquery"

export default function AgentsPage() {
	const { data, isLoading, error } = useQuery(listAgents, {})
	const agents = data?.agents ?? []

	return (
		<div className="space-y-6">
			<div className="flex items-center justify-between">
				<div>
					<h1 className="font-semibold text-2xl tracking-tight">Agents</h1>
					<p className="text-muted-foreground">Running and stopped agents managed by ottersd</p>
				</div>
				<Button asChild>
					<Link href="/agents/new">
						<Plus className="mr-2 h-4 w-4" />
						Create Agent
					</Link>
				</Button>
			</div>

			{error && (
				<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
					Failed to reach daemon: {error.message}. Make sure{" "}
					<code className="font-mono">ottersd serve --http 127.0.0.1:5000</code> is running.
				</div>
			)}

			{isLoading && <p className="text-muted-foreground">Loading agents…</p>}

			{!isLoading && !error && agents.length > 0 && (
				<div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
					{agents.map((agent) => (
						<AgentCard agent={agent} key={agent.name} />
					))}
				</div>
			)}

			{!isLoading && !error && agents.length === 0 && (
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
