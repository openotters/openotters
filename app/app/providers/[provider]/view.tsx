"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { ArrowLeft, Cpu, ExternalLink, Key, Pencil, Plug, Trash2 } from "lucide-react"
import Link from "next/link"
import { notFound, useRouter } from "next/navigation"
import { useMemo, useState } from "react"
import { SortSelect, type SortOption } from "@/components/sort-select"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Separator } from "@/components/ui/separator"
import type { Model } from "@/lib/proto/v1/daemon_pb"
import {
	listModels,
	listProviders,
	removeProvider,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"
import { useRouteParams } from "@/lib/use-route-params"
import { useStableSort } from "@/lib/use-stable-sort"

const COMPACT_NUMBER = new Intl.NumberFormat("en", { notation: "compact" })

const SORT_OPTIONS: SortOption[] = [
	{ id: "name-asc", label: "Name (A→Z)" },
	{ id: "name-desc", label: "Name (Z→A)" },
	{ id: "ctx-desc", label: "Context window (largest)" },
	{ id: "ctx-asc", label: "Context window (smallest)" },
	{ id: "cost-asc", label: "Cost (cheapest input)" },
	{ id: "cost-desc", label: "Cost (priciest input)" },
]

const NAME_ASC = (a: Model, b: Model) =>
	(a.displayName || a.name).localeCompare(b.displayName || b.name)

function compareFor(sortId: string): (a: Model, b: Model) => number {
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
			return NAME_ASC
	}
}

// safeHost returns the hostname portion of url if parseable; otherwise
// echoes the input. `${ENV_VAR}` and other placeholders won't parse.
function safeHost(url: string): string {
	try {
		return new URL(url).hostname
	} catch {
		return url
	}
}

export default function ProviderDetailPage() {
	const params = useRouteParams<{ provider: string }>("/providers/:provider")
	const name = params.provider ?? ""
	const router = useRouter()
	const queryClient = useQueryClient()

	const providers = useQuery(listProviders, {}, { enabled: name !== "" })
	const models = useQuery(listModels, {}, { enabled: name !== "" })

	const remove = useMutation(removeProvider, {
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["openotters.daemon.v1.Runtime", "ListProviders"] })
			router.push("/providers")
		},
	})

	const [sortId, setSortId] = useState<string>("name-asc")

	const provider = providers.data?.providers.find((p) => p.name === name)
	const providerModels = useMemo(
		() => (models.data?.models ?? []).filter((m) => m.provider === name),
		[models.data, name],
	)

	const compare = useMemo(() => compareFor(sortId), [sortId])
	const sortedModels = useStableSort<Model>(providerModels, (m) => m.ref, compare)

	if (name === "" || providers.isLoading) {
		return <p className="text-muted-foreground">Loading provider…</p>
	}

	if (providers.error) {
		return (
			<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
				Failed to reach daemon: {providers.error.message}
			</div>
		)
	}

	if (providers.data && !provider) {
		notFound()
	}

	if (!provider) {
		return null
	}

	return (
		<div className="space-y-6">
			<div className="flex items-center gap-4">
				<Button asChild size="icon" variant="ghost">
					<Link href="/providers">
						<ArrowLeft className="h-4 w-4" />
					</Link>
				</Button>
				<div className="flex flex-1 items-center gap-3">
					<div className="flex h-12 w-12 items-center justify-center rounded-lg bg-primary/10">
						<Plug className="h-6 w-6 text-primary" />
					</div>
					<div>
						<h1 className="font-semibold text-2xl tracking-tight">{provider.name}</h1>
						{provider.apiBase && (
							<p className="flex items-center gap-1 font-mono text-muted-foreground text-sm">
								<ExternalLink className="h-3 w-3" />
								{safeHost(provider.apiBase)}
							</p>
						)}
					</div>
				</div>
				<div className="flex items-center gap-2">
					<Button asChild size="sm" variant="outline">
						<Link href={`/providers/${provider.name}/edit`}>
							<Pencil className="mr-2 h-4 w-4" />
							Edit
						</Link>
					</Button>
					<Button asChild size="sm" variant="outline">
						<Link href={`/providers/${provider.name}/edit#auth`}>
							<Key className="mr-2 h-4 w-4" />
							Update API Key
						</Link>
					</Button>
					<Button
						className="text-destructive hover:text-destructive"
						disabled={remove.isPending}
						onClick={() => remove.mutate({ name: provider.name })}
						size="sm"
						variant="outline">
						<Trash2 className="mr-2 h-4 w-4" />
						Remove
					</Button>
				</div>
			</div>

			<Card>
				<CardHeader>
					<CardTitle className="flex items-center gap-2 text-base">
						<Plug className="h-4 w-4" />
						Configuration
					</CardTitle>
					<CardDescription>
						Pulled from <code className="font-mono text-xs">~/.otters/providers.yaml</code> via the
						daemon's <code className="font-mono text-xs">ListProviders</code> RPC.
					</CardDescription>
				</CardHeader>
				<CardContent className="space-y-3 text-sm">
					<Row label="Name" mono value={provider.name} />
					<Separator />
					<Row label="API base" mono value={provider.apiBase || "—"} />
					<Separator />
					<Row
						label="Allow-list"
						value={
							provider.models.length === 0 ? (
								<span className="text-muted-foreground">all models allowed</span>
							) : (
								<div className="flex flex-wrap justify-end gap-1">
									{provider.models.map((m) => (
										<Badge className="font-mono text-xs" key={m} variant="secondary">
											{m}
										</Badge>
									))}
								</div>
							)
						}
					/>
				</CardContent>
			</Card>

			<div>
				<div className="mb-4 flex items-center justify-between gap-3">
					<div>
						<h2 className="font-semibold text-lg tracking-tight">Models</h2>
						<p className="text-muted-foreground text-sm">
							{models.isLoading
								? "Loading models…"
								: `${providerModels.length} advertised by ${provider.name}`}
						</p>
					</div>
					{providerModels.length > 0 && (
						<SortSelect
							className="w-[260px]"
							onValueChange={setSortId}
							options={SORT_OPTIONS}
							value={sortId}
						/>
					)}
				</div>

				{models.error && (
					<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
						Failed to load models: {models.error.message}
					</div>
				)}

				{!models.isLoading && !models.error && providerModels.length === 0 && (
					<Card className="border-dashed">
						<CardContent className="py-12 text-center text-muted-foreground">
							No models advertised. Either the provider's catalog is empty, or the API key /
							endpoint isn't returning a model list.
						</CardContent>
					</Card>
				)}

				{!models.error && sortedModels.length > 0 && (
					<div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
						{sortedModels.map((model) => (
							<Card className="group transition-colors hover:bg-muted/50" key={model.ref}>
								<CardHeader className="flex flex-row items-start justify-between space-y-0 pb-2">
									<div className="flex items-start gap-2">
										<Cpu className="mt-1 h-4 w-4 text-muted-foreground" />
										<div>
											<CardTitle className="text-base">
												{model.displayName || model.name}
											</CardTitle>
											<CardDescription className="font-mono text-xs">
												{model.ref}
											</CardDescription>
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
				)}
			</div>
		</div>
	)
}

interface RowProps {
	label: string
	value: React.ReactNode
	mono?: boolean
}

function Row({ label, value, mono }: RowProps) {
	return (
		<div className="flex items-start justify-between gap-6">
			<span className="shrink-0 text-muted-foreground">{label}</span>
			<span className={`break-all text-right ${mono ? "font-mono text-xs" : ""}`}>{value}</span>
		</div>
	)
}
