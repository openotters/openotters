"use client"

import { useQuery } from "@connectrpc/connect-query"
import { useMemo, useState } from "react"
import { JobsTable } from "@/components/jobs/jobs-table"
import { RunJobDialog } from "@/components/jobs/run-job-dialog"
import { PageHeader } from "@/components/page-header"
import { Input } from "@/components/ui/input"
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select"
import { listAsyncJobs } from "@/lib/proto/v1/daemon-Runtime_connectquery"

// Status filter values mirror the daemon's CHECK constraint on
// async_jobs.status. Empty string = no filter (the proto's
// "" sentinel).
const STATUSES = ["", "pending", "running", "done", "error", "cancelled", "orphaned"] as const

export default function JobsPage() {
	const [status, setStatus] = useState<string>("")
	const [labelFilter, setLabelFilter] = useState("")

	// Parse `key=value` (or empty) into the proto's label_selector
	// shape. Invalid syntax just renders an empty selector — the
	// table re-renders with everything that matches the rest of the
	// filter, so the user sees the result while they're typing.
	const labelSelector = useMemo<{ [key: string]: string }>(() => {
		const eq = labelFilter.indexOf("=")
		if (eq < 1) return {}
		return { [labelFilter.slice(0, eq).trim()]: labelFilter.slice(eq + 1).trim() }
	}, [labelFilter])

	const { data, isLoading, error } = useQuery(
		listAsyncJobs,
		{ status, labelSelector },
		// Refresh fast enough that running jobs visibly tick to done
		// in real-world time, slow enough that an idle dashboard
		// doesn't pummel the daemon.
		{ refetchInterval: 2_000 },
	)

	const jobs = data?.jobs ?? []

	return (
		<div className="space-y-6">
			<PageHeader
				actions={<RunJobDialog />}
				command="otters jobs ls"
				description="Async BIN jobs dispatched against agent spawn envs."
				title="Jobs"
			/>

			<div className="flex flex-wrap gap-3">
				<Select onValueChange={setStatus} value={status || "_all"}>
					<SelectTrigger className="w-[180px]">
						<SelectValue placeholder="All statuses" />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value="_all">All statuses</SelectItem>
						{STATUSES.filter((s) => s !== "").map((s) => (
							<SelectItem key={s} value={s}>
								{s}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
				<Input
					className="max-w-xs"
					onChange={(e) => setLabelFilter(e.target.value)}
					placeholder="Label filter — key=value"
					value={labelFilter}
				/>
			</div>

			{error && (
				<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
					Failed to reach daemon: {error.message}
				</div>
			)}

			{isLoading && <p className="text-muted-foreground">Loading jobs…</p>}

			{!isLoading && !error && <JobsTable jobs={jobs} />}
		</div>
	)
}
