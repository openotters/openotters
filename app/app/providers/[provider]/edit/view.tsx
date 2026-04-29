"use client"

import { useQuery } from "@connectrpc/connect-query"
import { ArrowLeft } from "lucide-react"
import Link from "next/link"
import { notFound } from "next/navigation"
import { ProviderForm } from "@/components/provider-form"
import { Button } from "@/components/ui/button"
import { listProviders } from "@/lib/proto/v1/daemon-Runtime_connectquery"
import { useRouteParams } from "@/lib/use-route-params"

export default function EditProviderPage() {
	const params = useRouteParams<{ provider: string }>("/providers/:provider/edit")
	const name = params.provider ?? ""
	const { data, isLoading, error } = useQuery(listProviders, {}, { enabled: name !== "" })

	if (name === "" || isLoading) {
		return <p className="text-muted-foreground">Loading provider…</p>
	}

	if (error) {
		return (
			<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
				Failed to reach daemon: {error.message}
			</div>
		)
	}

	const provider = data?.providers.find((p) => p.name === name)
	if (!provider) {
		notFound()
	}

	return (
		<div className="space-y-6">
			<div className="flex items-center gap-4">
				<Button asChild size="icon" variant="ghost">
					<Link href="/providers">
						<ArrowLeft className="h-4 w-4" />
					</Link>
				</Button>
				<div>
					<h1 className="font-semibold text-2xl tracking-tight">Edit {provider.name}</h1>
					<p className="text-muted-foreground">
						Update credentials, endpoint, or model allow-list. Saving sends an{" "}
						<code className="font-mono text-xs">UpdateProvider</code> RPC to the daemon.
					</p>
				</div>
			</div>
			<ProviderForm initial={provider} mode="edit" />
		</div>
	)
}
