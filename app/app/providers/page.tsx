"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { ExternalLink, Key, MoreVertical, Pencil, Plug, Plus, Trash2 } from "lucide-react"
import Link from "next/link"
import { useQueryClient } from "@tanstack/react-query"
import { useMemo } from "react"
import { PageHeader } from "@/components/page-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { listProviders, removeProvider } from "@/lib/proto/v1/daemon-Runtime_connectquery"

export default function ProvidersPage() {
	const queryClient = useQueryClient()
	const { data, isLoading, error } = useQuery(listProviders, {})
	const remove = useMutation(removeProvider, {
		onSuccess: () => {
			// Refetch the list immediately so the removed row disappears
			// without waiting for the next polling tick.
			queryClient.invalidateQueries({ queryKey: ["openotters.daemon.v1.Runtime", "ListProviders"] })
		},
	})

	const sorted = useMemo(
		() => [...(data?.providers ?? [])].sort((a, b) => a.name.localeCompare(b.name)),
		[data],
	)

	return (
		<div className="space-y-6">
			<PageHeader
				actions={
					<Button asChild>
						<Link href="/providers/new">
							<Plus className="mr-2 h-4 w-4" />
							Add Provider
						</Link>
					</Button>
				}
				command="otters provider ls"
				description={
					<>
						LLM providers configured in{" "}
						<code className="font-mono text-xs">~/.otters/providers.yaml</code>.
					</>
				}
				title="Providers"
			/>

			{error && (
				<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
					Failed to reach daemon: {error.message}
				</div>
			)}

			{isLoading && <p className="text-muted-foreground">Loading providers…</p>}

			{!isLoading && !error && sorted.length > 0 && (
				<div className="grid gap-4">
					{sorted.map((provider) => (
						<Card className="group transition-colors hover:bg-muted/50" key={provider.name}>
							<CardHeader className="pb-3">
								<div className="flex items-start justify-between">
									{/*
										Whole row is a click target into the detail page.
										The dropdown sits OUTSIDE the link so its trigger
										and items don't navigate away when clicked.
									*/}
									<Link
										aria-label={`Open ${provider.name} details`}
										className="flex flex-1 items-center gap-3"
										href={`/providers/${provider.name}`}>
										<div className="flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10">
											<Plug className="h-5 w-5 text-primary" />
										</div>
										<div>
											<CardTitle className="text-base">{provider.name}</CardTitle>
											{provider.apiBase && (
												<CardDescription className="flex items-center gap-1 text-xs">
													<ExternalLink className="h-3 w-3" />
													{safeHost(provider.apiBase)}
												</CardDescription>
											)}
										</div>
									</Link>
									<DropdownMenu>
										<DropdownMenuTrigger asChild>
											<Button className="h-8 w-8" size="icon" variant="ghost">
												<MoreVertical className="h-4 w-4" />
											</Button>
										</DropdownMenuTrigger>
										<DropdownMenuContent align="end">
											<DropdownMenuItem asChild>
												<Link href={`/providers/${provider.name}`}>
													<ExternalLink className="mr-2 h-4 w-4" />
													Details
												</Link>
											</DropdownMenuItem>
											<DropdownMenuItem asChild>
												<Link href={`/providers/${provider.name}/edit`}>
													<Pencil className="mr-2 h-4 w-4" />
													Edit
												</Link>
											</DropdownMenuItem>
											<DropdownMenuItem asChild>
												<Link href={`/providers/${provider.name}/edit#auth`}>
													<Key className="mr-2 h-4 w-4" />
													Update API Key
												</Link>
											</DropdownMenuItem>
											<DropdownMenuSeparator />
											<DropdownMenuItem
												className="text-destructive"
												disabled={remove.isPending}
												onClick={() => remove.mutate({ name: provider.name })}>
												<Trash2 className="mr-2 h-4 w-4" />
												Remove
											</DropdownMenuItem>
										</DropdownMenuContent>
									</DropdownMenu>
								</div>
							</CardHeader>
							<CardContent>
								<div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-muted-foreground text-sm">
									<span>
										{provider.models.length === 0
											? "All models allowed"
											: `Allow-list (${provider.models.length})`}
									</span>
									{provider.models.slice(0, 8).map((model) => (
										<Badge className="font-mono text-xs" key={model} variant="secondary">
											{model}
										</Badge>
									))}
									{provider.models.length > 8 && (
										<Badge className="text-xs" variant="outline">
											+{provider.models.length - 8} more
										</Badge>
									)}
								</div>
							</CardContent>
						</Card>
					))}
				</div>
			)}

			{!isLoading && !error && sorted.length === 0 && (
				<Card className="border-dashed">
					<CardContent className="flex flex-col items-center justify-center py-12">
						<Plug className="mb-4 h-12 w-12 text-muted-foreground" />
						<p className="mb-4 text-muted-foreground">
							No providers configured. Add your first provider to enable agents.
						</p>
						<Button asChild>
							<Link href="/providers/new">
								<Plus className="mr-2 h-4 w-4" />
								Add Provider
							</Link>
						</Button>
					</CardContent>
				</Card>
			)}
		</div>
	)
}

// safeHost returns the hostname portion of url if parseable; otherwise
// echoes the input. The hostname is the most useful piece for the card
// caption — full URLs cause line-wrap issues — but `${ENV_VAR}` and
// other placeholders don't parse, so we fall back gracefully instead
// of throwing.
function safeHost(url: string): string {
	try {
		return new URL(url).hostname
	} catch {
		return url
	}
}
