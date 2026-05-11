"use client"

import { useQuery } from "@connectrpc/connect-query"
import { Cpu, FolderOpen, ListChecks, Network, Settings2 } from "lucide-react"
import Link from "next/link"
import { JobsTable } from "@/components/jobs/jobs-table"
import { ResourceStats } from "@/components/resource-stats"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Separator } from "@/components/ui/separator"
import {
	getInfo,
	listAsyncJobs,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"

interface RowProps {
	label: string
	value: React.ReactNode
}

function Row({ label, value }: RowProps) {
	return (
		<div className="flex items-start justify-between gap-6 py-2">
			<span className="shrink-0 text-muted-foreground text-sm">{label}</span>
			<span className="break-all text-right font-mono text-sm">{value || "—"}</span>
		</div>
	)
}

export default function HomePage() {
	const { data, isLoading, error } = useQuery(getInfo, {})
	const jobs = useQuery(
		listAsyncJobs,
		{},
		// Poll every 5s so a long-running job's state surfaces on
		// the dashboard without a manual refresh — same cadence the
		// /jobs page uses.
		{ refetchInterval: 5_000 },
	)

	const reachable = !error && !isLoading && data !== undefined
	// Server returns all jobs; clip to 10 newest for the dashboard
	// card. Full list lives behind "View all".
	const recentJobs = (jobs.data?.jobs ?? []).slice(0, 10)

	return (
		<div className="space-y-6">
			<div>
				<h1 className="font-semibold text-2xl tracking-tight">Dashboard</h1>
				<p className="text-muted-foreground">
					Daemon diagnostics — equivalent to <code className="font-mono text-xs">otters info</code>
				</p>
			</div>

			<ResourceStats />

			<Card>
				<CardHeader className="flex flex-row items-start justify-between space-y-0">
					<div className="space-y-1">
						<CardTitle className="flex items-center gap-2 text-base">
							<ListChecks className="h-4 w-4" />
							Recent jobs
						</CardTitle>
						<CardDescription>
							Last 10 async runs across all agents. Click a row for
							the full job timeline.
						</CardDescription>
					</div>
					<Button asChild size="sm" variant="ghost">
						<Link href="/jobs">View all</Link>
					</Button>
				</CardHeader>
				<CardContent>
					{jobs.isLoading && (
						<p className="py-4 text-center text-muted-foreground text-sm">
							Loading jobs…
						</p>
					)}
					{jobs.error && (
						<p className="py-4 text-center text-destructive text-sm">
							Failed to fetch jobs: {jobs.error.message}
						</p>
					)}
					{!jobs.isLoading && !jobs.error && (
						<JobsTable agentColumn jobs={recentJobs} />
					)}
				</CardContent>
			</Card>

			<div className="grid gap-4 lg:grid-cols-2">
				<Card>
					<CardHeader className="flex flex-row items-start justify-between space-y-0">
						<CardTitle className="flex items-center gap-2 text-base">
							<Cpu className="h-4 w-4" />
							Daemon
						</CardTitle>
						{/* Health badge sits next to the title rather than as a
						    separate top-row card — same information, less visual
						    duplication. */}
						{isLoading && (
							<Badge className="gap-1.5" variant="outline">
								<span className="size-2 animate-pulse rounded-full bg-muted-foreground" />
								Connecting…
							</Badge>
						)}
						{reachable && (
							<Badge className="gap-1.5" variant="secondary">
								<span className="size-2 rounded-full bg-emerald-500" />
								Healthy
							</Badge>
						)}
						{!reachable && !isLoading && (
							<Badge className="gap-1.5" variant="destructive">
								<span className="size-2 rounded-full bg-destructive-foreground" />
								Unreachable
							</Badge>
						)}
					</CardHeader>
					<CardContent>
						<Row label="Version" value={data?.version} />
						<Separator />
						<Row label="Commit" value={data?.commit} />
						<Separator />
						<Row label="Build date" value={data?.buildDate} />
						<Separator />
						<Row
							label="Executor"
							value={
								data?.executor ? (
									<Badge className="font-mono" variant="secondary">
										{data.executor}
									</Badge>
								) : undefined
							}
						/>
					</CardContent>
				</Card>

				<Card>
					<CardHeader>
						<CardTitle className="flex items-center gap-2 text-base">
							<Network className="h-4 w-4" />
							Sockets &amp; registry
						</CardTitle>
					</CardHeader>
					<CardContent>
						<Row label="Socket" value={data?.socketPath} />
						<Separator />
						<Row label="Registry" value={data?.registryAddr} />
					</CardContent>
				</Card>

				<Card className="lg:col-span-2">
					<CardHeader>
						<CardTitle className="flex items-center gap-2 text-base">
							<Settings2 className="h-4 w-4" />
							Pool &amp; shutdown
						</CardTitle>
					</CardHeader>
					<CardContent>
						<Row label="Max concurrent" value={data?.maxConcurrent} />
						<Separator />
						<Row label="Backoff base" value={data?.backoffBase} />
						<Separator />
						<Row label="Backoff cap" value={data?.backoffCap} />
						<Separator />
						<Row label="Shutdown timeout" value={data?.shutdownTimeout} />
					</CardContent>
				</Card>

				<Card className="lg:col-span-2">
					<CardHeader>
						<CardTitle className="flex items-center gap-2 text-base">
							<FolderOpen className="h-4 w-4" />
							Paths
						</CardTitle>
					</CardHeader>
					<CardContent>
						<Row label="Data dir" value={data?.dataDir} />
						<Separator />
						<Row label="Agents dir" value={data?.agentsDir} />
						<Separator />
						<Row label="Log dir" value={data?.logDir} />
						<Separator />
						<Row label="Runtime path" value={data?.runtimePath} />
					</CardContent>
				</Card>
			</div>
		</div>
	)
}
