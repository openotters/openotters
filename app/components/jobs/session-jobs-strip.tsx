"use client"

import { useQuery } from "@connectrpc/connect-query"
import { ChevronDown, ChevronRight } from "lucide-react"
import Link from "next/link"
import { useState } from "react"
import { StatusBadge } from "@/components/status-badge"
import type { AsyncJob } from "@/lib/proto/v1/daemon_pb"
import { listAsyncJobs } from "@/lib/proto/v1/daemon-Runtime_connectquery"

interface SessionJobsStripProps {
	sessionId: string
}

// SessionJobsStrip shows the most recent async job tagged with this
// chat session as a chip beneath the chat header. The runtime
// auto-stamps io.openotters.session-id on every job_submit call
// (see runtime/pkg/tool/jobs.go), so any tool-driven submission
// from this conversation appears here automatically.
//
// Collapsed by default to the single newest job — long-running
// sessions can accumulate dozens of jobs and a wall of chips
// dominates the chat header. A chevron toggle expands the strip
// to the full list.
//
// Hidden when the session has no jobs — the strip is purely a
// progress affordance, not a permanent UI element. Polls 2s so a
// running job visibly ticks to done without manual refresh.
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

	const [expanded, setExpanded] = useState(false)

	const jobs = data?.jobs ?? []
	if (jobs.length === 0) {
		return null
	}

	// Sort newest-first so the head of the list is the latest job;
	// listAsyncJobs returns server-order which isn't guaranteed.
	const sorted = [...jobs].sort((a, b) =>
		Number((b.createdAt ?? 0n) - (a.createdAt ?? 0n)),
	)
	// Collapsed default: a small handful (3) so an active session
	// shows enough context without paving the chat header. Expand
	// reveals the full list.
	const visible = expanded ? sorted : sorted.slice(0, 3)
	const hidden = sorted.length - visible.length

	return (
		<div className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-muted/30 px-6 py-2 text-xs">
			<span className="text-muted-foreground">
				{sorted.length === 1
					? "Job in this session:"
					: `Jobs in this session (${sorted.length}):`}
			</span>
			{visible.map((job) => (
				<JobChip job={job} key={job.id} />
			))}
			{hidden > 0 && (
				<button
					className="inline-flex items-center gap-1 rounded-md border bg-background px-2 py-1 text-muted-foreground hover:bg-accent hover:text-foreground"
					onClick={() => setExpanded(true)}
					type="button">
					<ChevronRight className="h-3 w-3" />
					Show {hidden} more
				</button>
			)}
			{expanded && sorted.length > 1 && (
				<button
					className="inline-flex items-center gap-1 rounded-md border bg-background px-2 py-1 text-muted-foreground hover:bg-accent hover:text-foreground"
					onClick={() => setExpanded(false)}
					type="button">
					<ChevronDown className="h-3 w-3" />
					Collapse
				</button>
			)}
		</div>
	)
}

function JobChip({ job }: { job: AsyncJob }) {
	return (
		<Link
			className="inline-flex items-center gap-1.5 rounded-md border bg-background px-2 py-1 hover:bg-accent"
			href={`/jobs/${job.id}`}
			title={`${job.bin} ${job.args.join(" ")}`}>
			<StatusBadge status={job.status} />
			<span className="font-mono text-muted-foreground">{shortJobID(job.id)}</span>
			<span className="font-mono">{job.bin}</span>
		</Link>
	)
}

function shortJobID(id: string): string {
	const stripped = id.startsWith("job_") ? id.slice(4) : id
	return stripped.length > 8 ? stripped.slice(0, 8) : stripped
}
