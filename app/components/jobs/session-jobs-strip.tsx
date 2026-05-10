"use client"

import { useQuery } from "@connectrpc/connect-query"
import Link from "next/link"
import { StatusBadge } from "@/components/status-badge"
import { listAsyncJobs } from "@/lib/proto/v1/daemon-Runtime_connectquery"

interface SessionJobsStripProps {
	sessionId: string
}

// SessionJobsStrip shows every async job tagged with this chat
// session as a horizontal chip row beneath the chat header. The
// runtime auto-stamps io.openotters.session-id on every job_submit
// call (see runtime/pkg/tool/jobs.go), so any tool-driven submission
// from this conversation appears here automatically.
//
// Hidden when the session has no jobs — the strip is purely a
// progress affordance, not a permanent UI element. Polls 2s so a
// running job visibly ticks to done without manual refresh.
//
// Each chip links to /jobs/<id> for the full detail view (logs,
// cancel button, lifecycle timestamps).
export function SessionJobsStrip({ sessionId }: SessionJobsStripProps) {
	const { data } = useQuery(
		listAsyncJobs,
		{
			labelSelector: { "io.openotters.session-id": sessionId },
		},
		{
			enabled: sessionId !== "",
			refetchInterval: 2_000,
		},
	)

	const jobs = data?.jobs ?? []
	if (jobs.length === 0) {
		return null
	}

	return (
		<div className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-muted/30 px-6 py-2 text-xs">
			<span className="text-muted-foreground">Jobs in this session:</span>
			{jobs.map((job) => (
				<Link
					className="inline-flex items-center gap-1.5 rounded-md border bg-background px-2 py-1 hover:bg-accent"
					href={`/jobs/${job.id}`}
					key={job.id}
					title={`${job.bin} ${job.args.join(" ")}`}>
					<StatusBadge status={job.status} />
					<span className="font-mono text-muted-foreground">{shortJobID(job.id)}</span>
					<span className="font-mono">{job.bin}</span>
				</Link>
			))}
		</div>
	)
}

function shortJobID(id: string): string {
	const stripped = id.startsWith("job_") ? id.slice(4) : id
	return stripped.length > 8 ? stripped.slice(0, 8) : stripped
}
