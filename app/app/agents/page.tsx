"use client"

import { useQuery } from "@connectrpc/connect-query"
import { Plus, Search } from "lucide-react"
import Link from "next/link"
import { useMemo, useState } from "react"
import { AgentCard } from "@/components/agent-card"
import { SortSelect, SORT_DEFAULT_ID, type SortOption } from "@/components/sort-select"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { AgentInfo } from "@/lib/proto/v1/daemon_pb"
import { listAgents } from "@/lib/proto/v1/daemon-Runtime_connectquery"
import { useStableSort } from "@/lib/use-stable-sort"

const SORT_OPTIONS: SortOption[] = [
	{ id: "name-asc", label: "Name (A→Z)" },
	{ id: "name-desc", label: "Name (Z→A)" },
	{ id: "status", label: "Status (running first)" },
	{ id: "newest", label: "Newest first" },
	{ id: "oldest", label: "Oldest first" },
]

// Higher = appears first when sorting by status. Running agents are
// the most actionable, then anything that hasn't reached a steady
// state yet, then errored / stopped ones.
const STATUS_RANK: Record<string, number> = {
	running: 0,
	pending: 1,
	created: 2,
	model_error: 3,
	init_error: 3,
	pull_error: 3,
	stopped: 4,
}

function compareFor(sortId: string): ((a: AgentInfo, b: AgentInfo) => number) | null {
	switch (sortId) {
		case "name-asc":
			return (a, b) => a.name.localeCompare(b.name)
		case "name-desc":
			return (a, b) => b.name.localeCompare(a.name)
		case "status":
			return (a, b) =>
				(STATUS_RANK[a.status] ?? 99) - (STATUS_RANK[b.status] ?? 99) ||
				a.name.localeCompare(b.name)
		case "newest":
			return (a, b) => Number(b.createdAt - a.createdAt)
		case "oldest":
			return (a, b) => Number(a.createdAt - b.createdAt)
		default:
			return null
	}
}

export default function AgentsPage() {
	const { data, isLoading, error } = useQuery(listAgents, {})
	const agents = data?.agents ?? []

	const [sortId, setSortId] = useState<string>(SORT_DEFAULT_ID)
	const [search, setSearch] = useState<string>("")

	const sorted = useStableSort<AgentInfo>(
		agents,
		(a) => a.id,
		useMemo(() => ({ compare: compareFor(sortId) }), [sortId]),
	)

	const filtered = useMemo(() => {
		const q = search.trim().toLowerCase()
		if (!q) return sorted
		return sorted.filter(
			(a) =>
				a.name.toLowerCase().includes(q) ||
				a.model.toLowerCase().includes(q) ||
				a.status.toLowerCase().includes(q),
		)
	}, [sorted, search])

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

			{!error && (
				<div className="flex flex-wrap items-center gap-3">
					<div className="relative max-w-sm flex-1">
						<Search className="-translate-y-1/2 absolute top-1/2 left-3 h-4 w-4 text-muted-foreground" />
						<Input
							className="pl-9"
							onChange={(e) => setSearch(e.target.value)}
							placeholder="Search agents…"
							value={search}
						/>
					</div>
					<SortSelect
						className="w-[220px]"
						onValueChange={setSortId}
						options={SORT_OPTIONS}
						value={sortId}
					/>
				</div>
			)}

			{isLoading && <p className="text-muted-foreground">Loading agents…</p>}

			{!isLoading && !error && filtered.length > 0 && (
				<div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
					{filtered.map((agent) => (
						<AgentCard agent={agent} key={agent.id} />
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

			{!isLoading && !error && agents.length > 0 && filtered.length === 0 && (
				<div className="flex flex-col items-center justify-center rounded-lg border border-dashed py-12">
					<p className="text-muted-foreground">No agents match the search.</p>
				</div>
			)}
		</div>
	)
}
