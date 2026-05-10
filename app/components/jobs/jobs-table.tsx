"use client"

import Link from "next/link"
import type { AsyncJob } from "@/lib/proto/v1/daemon_pb"
import { StatusBadge } from "@/components/status-badge"
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table"
import { LabelsDisplay } from "./labels-display"

interface JobsTableProps {
	jobs: AsyncJob[]
	// agentColumn — when false, the AGENT column is hidden. Used by
	// agent-scoped views (the "Related jobs" card on the agent detail
	// page) where every row is for the same agent and the column
	// would just be noise.
	agentColumn?: boolean
}

// JobsTable renders a list of async jobs newest-first. Status badges
// are clickable rows linking to /jobs/<id>. Duration is computed
// from started_at / finished_at — running jobs show "running" so the
// reader doesn't need to mentally diff timestamps.
export function JobsTable({ jobs, agentColumn = true }: JobsTableProps) {
	if (jobs.length === 0) {
		return (
			<p className="rounded-lg border border-dashed p-6 text-center text-muted-foreground text-sm">
				No jobs yet.
			</p>
		)
	}

	return (
		<div className="overflow-hidden rounded-lg border">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>ID</TableHead>
						{agentColumn && <TableHead>Agent</TableHead>}
						<TableHead>Bin</TableHead>
						<TableHead>Status</TableHead>
						<TableHead>Created</TableHead>
						<TableHead>Duration</TableHead>
						<TableHead>Labels</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{jobs.map((job) => (
						<TableRow key={job.id} className="cursor-pointer hover:bg-muted/50">
							<TableCell className="font-mono text-xs">
								<Link className="hover:underline" href={`/jobs/${job.id}`}>
									{shortJobID(job.id)}
								</Link>
							</TableCell>
							{agentColumn && (
								<TableCell className="font-mono text-xs text-muted-foreground">
									{shortAgentID(job.agentId)}
								</TableCell>
							)}
							<TableCell className="font-mono text-xs">{job.bin}</TableCell>
							<TableCell>
								<StatusBadge status={job.status} />
							</TableCell>
							<TableCell
								className="text-xs text-muted-foreground"
								title={formatAbsolute(job.createdAt)}>
								{formatRelative(job.createdAt)}
							</TableCell>
							<TableCell className="text-xs text-muted-foreground">
								{formatDuration(job)}
							</TableCell>
							<TableCell>
								<LabelsDisplay labels={job.labels} compact />
							</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
		</div>
	)
}

// shortJobID strips the `job_` prefix and truncates the UUID for table
// display. Detail page shows the full ID.
function shortJobID(id: string): string {
	const stripped = id.startsWith("job_") ? id.slice(4) : id
	return stripped.length > 8 ? stripped.slice(0, 8) : stripped
}

function shortAgentID(id: string): string {
	return id.length > 8 ? id.slice(0, 8) : id
}

// formatRelative renders a unix timestamp as "5s ago" / "3m ago" /
// "2h ago" / "4d ago". Pleasant for recently-created rows; the
// absolute time is on the cell's `title` attribute for the rare
// case where a precise timestamp matters.
function formatRelative(unixSec: bigint): string {
	if (unixSec === 0n) return "-"
	const ms = Date.now() - Number(unixSec) * 1000
	if (ms < 0) return "in the future"
	const sec = Math.floor(ms / 1000)
	if (sec < 60) return `${sec}s ago`
	const min = Math.floor(sec / 60)
	if (min < 60) return `${min}m ago`
	const hr = Math.floor(min / 60)
	if (hr < 24) return `${hr}h ago`
	return `${Math.floor(hr / 24)}d ago`
}

function formatAbsolute(unixSec: bigint): string {
	if (unixSec === 0n) return ""
	return new Date(Number(unixSec) * 1000).toLocaleString()
}

// formatDuration renders started→finished as a coarse human duration
// ("1.2s", "47s", "3m"). Pending jobs render "-"; running jobs render
// "running" since the wall-clock would tick on every poll and the
// table would jitter.
function formatDuration(job: AsyncJob): string {
	if (job.startedAt === 0n) {
		return "-"
	}
	if (job.finishedAt === 0n) {
		return "running"
	}
	const ms = (Number(job.finishedAt) - Number(job.startedAt)) * 1000
	if (ms < 1000) return `${ms}ms`
	if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
	return `${Math.round(ms / 60_000)}m`
}
