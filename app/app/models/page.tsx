"use client"

import { useQuery } from "@connectrpc/connect-query"
import { Cpu, Plug } from "lucide-react"
import { useMemo, useState } from "react"
import { SortSelect, type SortOption } from "@/components/sort-select"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import type { Model } from "@/lib/proto/v1/daemon_pb"
import { listModels } from "@/lib/proto/v1/daemon-Runtime_connectquery"
import { useStableSort } from "@/lib/use-stable-sort"

const COMPACT_NUMBER = new Intl.NumberFormat("en", { notation: "compact" })

const PROVIDER_SORT_OPTIONS: SortOption[] = [
	{ id: "name-asc", label: "Name (A→Z)" },
	{ id: "name-desc", label: "Name (Z→A)" },
]

const MODEL_SORT_OPTIONS: SortOption[] = [
	{ id: "name-asc", label: "Name (A→Z)" },
	{ id: "name-desc", label: "Name (Z→A)" },
	{ id: "ctx-desc", label: "Context window (largest)" },
	{ id: "ctx-asc", label: "Context window (smallest)" },
	{ id: "cost-asc", label: "Cost (cheapest input)" },
	{ id: "cost-desc", label: "Cost (priciest input)" },
]

interface ProviderGroup {
	name: string
	models: Model[]
}

const PROVIDER_NAME_ASC = (a: ProviderGroup, b: ProviderGroup) =>
	a.name.localeCompare(b.name)

function compareProviders(sortId: string): (a: ProviderGroup, b: ProviderGroup) => number {
	switch (sortId) {
		case "name-desc":
			return (a, b) => b.name.localeCompare(a.name)
		default:
			return PROVIDER_NAME_ASC
	}
}

const MODEL_NAME_ASC = (a: Model, b: Model) =>
	(a.displayName || a.name).localeCompare(b.displayName || b.name)

function compareModels(sortId: string): (a: Model, b: Model) => number {
	switch (sortId) {
		case "name-desc":
			return (a, b) => (b.displayName || b.name).localeCompare(a.displayName || a.name)
		case "ctx-desc":
			return (a, b) => Number(b.contextWindow - a.contextWindow)
		case "ctx-asc":
			return (a, b) => Number(a.contextWindow - b.contextWindow)
		case "cost-asc":
			return (a, b) => a.costInputPer1m - b.costInputPer1m
		case "cost-desc":
			return (a, b) => b.costInputPer1m - a.costInputPer1m
		default:
			return MODEL_NAME_ASC
	}
}

// SortedModelGroup wraps the per-provider model array with its own
// stable-sort hook call. Hooks can't run inside a render loop, so we
// move each group into its own component to keep React's hook order
// rules happy when the number of providers changes between renders.
function SortedModelGroup({ provider, models, sortId }: { provider: string; models: Model[]; sortId: string }) {
	const compare = useMemo(() => compareModels(sortId), [sortId])
	const sorted = useStableSort<Model>(models, (m) => m.ref, compare)

	return (
		<div className="space-y-3">
			<div className="flex items-center gap-3">
				<div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10">
					<Plug className="h-4 w-4 text-primary" />
				</div>
				<div className="flex-1">
					<h2 className="font-semibold">{provider}</h2>
					<p className="font-mono text-muted-foreground text-xs">
						{sorted.length} model{sorted.length === 1 ? "" : "s"}
					</p>
				</div>
			</div>
			<div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
				{sorted.map((model) => (
					<Card className="group transition-colors hover:bg-muted/50" key={model.ref}>
						<CardHeader className="flex flex-row items-start justify-between space-y-0 pb-2">
							<div className="flex items-start gap-2">
								<Cpu className="mt-1 h-4 w-4 text-muted-foreground" />
								<div>
									<CardTitle className="text-base">
										{model.displayName || model.name}
									</CardTitle>
									<CardDescription className="font-mono text-xs">{model.ref}</CardDescription>
								</div>
							</div>
						</CardHeader>
						<CardContent className="flex flex-wrap gap-2">
							{Number(model.contextWindow) > 0 && (
								<Badge className="font-mono text-xs" variant="secondary">
									{COMPACT_NUMBER.format(Number(model.contextWindow))} ctx
								</Badge>
							)}
							{model.canReason && (
								<Badge className="text-xs" variant="outline">
									reasons
								</Badge>
							)}
							{model.costInputPer1m > 0 && (
								<Badge className="font-mono text-xs" variant="outline">
									in ${model.costInputPer1m.toFixed(2)}/1M
								</Badge>
							)}
							{model.costOutputPer1m > 0 && (
								<Badge className="font-mono text-xs" variant="outline">
									out ${model.costOutputPer1m.toFixed(2)}/1M
								</Badge>
							)}
						</CardContent>
					</Card>
				))}
			</div>
		</div>
	)
}

export default function ModelsPage() {
	const { data, isLoading, error } = useQuery(listModels, {})
	const models = data?.models ?? []

	const [providerSortId, setProviderSortId] = useState<string>("name-asc")
	const [modelSortId, setModelSortId] = useState<string>("name-asc")

	// Group flattened models by provider; the proto returns one row per
	// model, but the page renders per-provider sections.
	const groups = useMemo<ProviderGroup[]>(() => {
		const byProvider = new Map<string, Model[]>()
		for (const model of models) {
			const arr = byProvider.get(model.provider) ?? []
			arr.push(model)
			byProvider.set(model.provider, arr)
		}
		return [...byProvider.entries()].map(([name, m]) => ({ name, models: m }))
	}, [models])

	const compareGroups = useMemo(() => compareProviders(providerSortId), [providerSortId])
	const sortedGroups = useStableSort<ProviderGroup>(groups, (g) => g.name, compareGroups)

	return (
		<div className="space-y-6">
			<div>
				<h1 className="font-semibold text-2xl tracking-tight">Models</h1>
				<p className="text-muted-foreground">
					Models advertised by configured providers — {models.length} total. Equivalent to{" "}
					<code className="font-mono text-xs">otters models ls</code>.
				</p>
			</div>

			{error && (
				<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
					Failed to reach daemon: {error.message}
				</div>
			)}

			{!error && groups.length > 0 && (
				<div className="flex flex-wrap items-center gap-3">
					<SortSelect
						className="w-[220px]"
						onValueChange={setProviderSortId}
						options={PROVIDER_SORT_OPTIONS}
						value={providerSortId}
					/>
					<SortSelect
						className="w-[260px]"
						onValueChange={setModelSortId}
						options={MODEL_SORT_OPTIONS}
						value={modelSortId}
					/>
				</div>
			)}

			{isLoading && <p className="text-muted-foreground">Loading models…</p>}

			{!isLoading && !error && groups.length === 0 && (
				<Card className="border-dashed">
					<CardContent className="py-12 text-center text-muted-foreground">
						No models advertised. Add a provider to populate this list.
					</CardContent>
				</Card>
			)}

			<div className="space-y-6">
				{sortedGroups.map((group) => (
					<SortedModelGroup
						key={group.name}
						models={group.models}
						provider={group.name}
						sortId={modelSortId}
					/>
				))}
			</div>
		</div>
	)
}
