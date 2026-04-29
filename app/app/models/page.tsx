"use client"

import { useQuery } from "@connectrpc/connect-query"
import { Cpu, Plug } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import type { Model } from "@/lib/proto/v1/daemon_pb"
import { listModels } from "@/lib/proto/v1/daemon-Runtime_connectquery"

const COMPACT_NUMBER = new Intl.NumberFormat("en", { notation: "compact" })

export default function ModelsPage() {
	const { data, isLoading, error } = useQuery(listModels, {})
	const models = data?.models ?? []

	// Group flattened models by provider; the proto returns one row per
	// model, but the page renders per-provider sections.
	const byProvider = new Map<string, Model[]>()
	for (const model of models) {
		const arr = byProvider.get(model.provider) ?? []
		arr.push(model)
		byProvider.set(model.provider, arr)
	}

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

			{isLoading && <p className="text-muted-foreground">Loading models…</p>}

			{!isLoading && !error && byProvider.size === 0 && (
				<Card className="border-dashed">
					<CardContent className="py-12 text-center text-muted-foreground">
						No models advertised. Add a provider to populate this list.
					</CardContent>
				</Card>
			)}

			<div className="space-y-6">
				{[...byProvider.entries()].map(([provider, providerModels]) => (
					<div className="space-y-3" key={provider}>
						<div className="flex items-center gap-3">
							<div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10">
								<Plug className="h-4 w-4 text-primary" />
							</div>
							<div className="flex-1">
								<h2 className="font-semibold">{provider}</h2>
								<p className="font-mono text-muted-foreground text-xs">
									{providerModels.length} model{providerModels.length === 1 ? "" : "s"}
								</p>
							</div>
						</div>
						<div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
							{providerModels.map((model) => (
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
					</div>
				))}
			</div>
		</div>
	)
}
