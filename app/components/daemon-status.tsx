"use client"

import { useQuery } from "@connectrpc/connect-query"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { getInfo } from "@/lib/proto/v1/daemon-Runtime_connectquery"
import { cn } from "@/lib/utils"

// Compact daemon-health pill for the sidebar / header. Uses the same
// GetInfo query the dashboard uses, so the cache is shared — no extra
// RPC traffic. Tooltip surfaces the daemon's reported version + a
// recovery hint when unreachable.
export function DaemonStatus() {
	const { data, isLoading, error } = useQuery(getInfo, {}, { refetchInterval: 5_000 })

	const reachable = !error && !isLoading && data !== undefined

	const dotClass = isLoading
		? "bg-muted-foreground animate-pulse"
		: reachable
			? "bg-emerald-500"
			: "bg-destructive"

	const label = isLoading
		? "Connecting…"
		: reachable
			? `Daemon v${data?.version || "unknown"}`
			: "Daemon unreachable"

	return (
		<TooltipProvider>
			<Tooltip>
				<TooltipTrigger asChild>
					<div className="flex items-center gap-2 rounded-md px-2 py-1 text-xs">
						<span className={cn("size-2 rounded-full", dotClass)} />
						<span className="text-muted-foreground">{label}</span>
					</div>
				</TooltipTrigger>
				<TooltipContent align="start" side="bottom">
					{reachable ? (
						<div className="space-y-1 text-xs">
							<p className="font-medium">ottersd reachable</p>
							{data?.socketPath && (
								<p className="font-mono text-muted-foreground">{data.socketPath}</p>
							)}
							{data?.registryAddr && (
								<p className="font-mono text-muted-foreground">
									registry: {data.registryAddr}
								</p>
							)}
						</div>
					) : (
						<div className="space-y-1 text-xs">
							<p className="font-medium">Cannot reach ottersd</p>
							<p className="text-muted-foreground">
								Run <code className="font-mono">ottersd serve --http 127.0.0.1:5500</code>
							</p>
							{error?.message && <p className="text-muted-foreground">{error.message}</p>}
						</div>
					)}
				</TooltipContent>
			</Tooltip>
		</TooltipProvider>
	)
}
