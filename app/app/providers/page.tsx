"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { ExternalLink, Key, MoreVertical, Pencil, Plug, Plus, Trash2 } from "lucide-react"
import Link from "next/link"
import { useQueryClient } from "@tanstack/react-query"
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

	const providers = data?.providers ?? []

	return (
		<div className="space-y-6">
			<div className="flex items-center justify-between">
				<div>
					<h1 className="font-semibold text-2xl tracking-tight">Providers</h1>
					<p className="text-muted-foreground">
						LLM providers configured in <code className="font-mono text-xs">~/.otters/providers.yaml</code>
					</p>
				</div>
				<Button asChild>
					<Link href="/providers/new">
						<Plus className="mr-2 h-4 w-4" />
						Add Provider
					</Link>
				</Button>
			</div>

			{error && (
				<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
					Failed to reach daemon: {error.message}
				</div>
			)}

			{isLoading && <p className="text-muted-foreground">Loading providers…</p>}

			{!isLoading && !error && providers.length > 0 && (
				<div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
					{providers.map((provider) => (
						<Card className="group" key={provider.name}>
							<CardHeader className="flex flex-row items-start justify-between space-y-0 pb-2">
								<div className="flex items-start gap-3">
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
								</div>
								<DropdownMenu>
									<DropdownMenuTrigger asChild>
										<Button className="h-8 w-8" size="icon" variant="ghost">
											<MoreVertical className="h-4 w-4" />
										</Button>
									</DropdownMenuTrigger>
									<DropdownMenuContent align="end">
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
							</CardHeader>
							<CardContent className="space-y-3">
								<div className="space-y-2">
									<span className="text-muted-foreground text-sm">
										{provider.models.length === 0
											? "All models allowed"
											: `Allow-list (${provider.models.length})`}
									</span>
									<div className="flex flex-wrap gap-1">
										{provider.models.slice(0, 5).map((model) => (
											<Badge className="font-mono text-xs" key={model} variant="secondary">
												{model}
											</Badge>
										))}
										{provider.models.length > 5 && (
											<Badge className="text-xs" variant="outline">
												+{provider.models.length - 5} more
											</Badge>
										)}
									</div>
								</div>
							</CardContent>
						</Card>
					))}
				</div>
			)}

			{!isLoading && !error && providers.length === 0 && (
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
