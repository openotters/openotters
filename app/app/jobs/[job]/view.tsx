"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { ArrowLeft, Square } from "lucide-react"
import Link from "next/link"
import { toast } from "sonner"
import { LabelsDisplay } from "@/components/jobs/labels-display"
import { PageHeader } from "@/components/page-header"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Separator } from "@/components/ui/separator"
import { useRouteParams } from "@/lib/use-route-params"
import { cancelAsyncJob, getAsyncJob } from "@/lib/proto/v1/daemon-Runtime_connectquery"

const TERMINAL = new Set(["done", "error", "cancelled", "orphaned"])

export default function JobDetailView() {
	const params = useRouteParams<{ job: string }>("/jobs/:job")
	const jobId = params.job ?? ""
	const queryClient = useQueryClient()

	const { data, isLoading, error } = useQuery(
		getAsyncJob,
		{ jobId },
		{
			enabled: jobId !== "",
			// Poll while non-terminal; the select callback returns the
			// row's status string, so React Query stops the interval
			// once the job reaches a terminal state. Avoids burning
			// requests on long-completed jobs.
			refetchInterval: (query) => {
				const status = query.state.data?.job?.status
				if (status && TERMINAL.has(status)) return false
				return 1_000
			},
		},
	)

	const cancel = useMutation(cancelAsyncJob, {
		onSuccess: () => {
			queryClient.invalidateQueries({
				queryKey: ["openotters.daemon.v1.Runtime", "GetAsyncJob"],
			})
			toast.success(`Cancellation requested for ${jobId}`)
		},
		onError: (err) => {
			toast.error("Cancel failed", { description: err.message })
		},
	})

	if (jobId === "" || isLoading) {
		return <p className="text-muted-foreground">Loading job…</p>
	}

	if (error) {
		return (
			<div className="space-y-4">
				<Button asChild size="sm" variant="ghost">
					<Link href="/jobs">
						<ArrowLeft className="mr-2 h-4 w-4" />
						Back to Jobs
					</Link>
				</Button>
				<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
					Failed to load job: {error.message}
				</div>
			</div>
		)
	}

	const job = data?.job
	if (!job) {
		return (
			<div className="space-y-4">
				<Button asChild size="sm" variant="ghost">
					<Link href="/jobs">
						<ArrowLeft className="mr-2 h-4 w-4" />
						Back to Jobs
					</Link>
				</Button>
				<p className="text-muted-foreground">Job not found.</p>
			</div>
		)
	}

	const isTerminal = TERMINAL.has(job.status)

	return (
		<div className="space-y-6">
			<Button asChild size="sm" variant="ghost">
				<Link href="/jobs">
					<ArrowLeft className="mr-2 h-4 w-4" />
					Back to Jobs
				</Link>
			</Button>

			<PageHeader
				actions={
					!isTerminal && (
						<Button
							disabled={cancel.isPending}
							onClick={() => cancel.mutate({ jobId })}
							size="sm"
							variant="outline">
							<Square className="mr-2 h-4 w-4" />
							Cancel
						</Button>
					)
				}
				command={`otters jobs inspect ${job.id}`}
				description={
					<span className="flex items-center gap-2">
						<StatusBadge status={job.status} />
						<span className="font-mono text-xs">{job.bin}</span>
					</span>
				}
				title={job.id}
			/>

			<div className="grid gap-4 md:grid-cols-2">
				<Card>
					<CardHeader>
						<CardTitle className="text-base">Spec</CardTitle>
					</CardHeader>
					<CardContent className="space-y-2 text-sm">
						<DetailRow label="Agent">
							<Link
								className="font-mono text-xs hover:underline"
								href={`/agents/${job.agentId}`}>
								{job.agentId}
							</Link>
						</DetailRow>
						<DetailRow label="Bin">
							<span className="font-mono text-xs">{job.bin}</span>
						</DetailRow>
						<DetailRow label="Args">
							<span className="font-mono text-xs">
								{job.args.length === 0 ? "-" : JSON.stringify(job.args)}
							</span>
						</DetailRow>
						{job.stdin !== "" && (
							<DetailRow label="Stdin">
								<pre className="overflow-x-auto rounded bg-muted px-2 py-1 font-mono text-xs">
									{job.stdin}
								</pre>
							</DetailRow>
						)}
						<Separator />
						<DetailRow label="Labels">
							<LabelsDisplay labels={job.labels} />
						</DetailRow>
					</CardContent>
				</Card>

				<Card>
					<CardHeader>
						<CardTitle className="text-base">Lifecycle</CardTitle>
					</CardHeader>
					<CardContent className="space-y-2 text-sm">
						<DetailRow label="Status">
							<StatusBadge status={job.status} />
						</DetailRow>
						{job.handle !== "" && (
							<DetailRow label="Handle">
								<span className="font-mono text-xs">{job.handle}</span>
							</DetailRow>
						)}
						{isTerminal && (
							<DetailRow label="Exit code">
								<span className="font-mono text-xs">{job.exitCode}</span>
							</DetailRow>
						)}
						<DetailRow label="Created">{formatUnix(job.createdAt)}</DetailRow>
						{job.startedAt !== 0n && (
							<DetailRow label="Started">{formatUnix(job.startedAt)}</DetailRow>
						)}
						{job.finishedAt !== 0n && (
							<DetailRow label="Finished">{formatUnix(job.finishedAt)}</DetailRow>
						)}
					</CardContent>
				</Card>
			</div>

			{job.error !== "" && (
				<Card className="border-destructive/40">
					<CardHeader>
						<CardTitle className="text-base text-destructive">Error</CardTitle>
					</CardHeader>
					<CardContent>
						<pre className="overflow-x-auto whitespace-pre-wrap font-mono text-xs">{job.error}</pre>
					</CardContent>
				</Card>
			)}

			{/* Logs are always visible — empty stdout/stderr is meaningful
			    (the BIN ran with no output), so a placeholder beats the
			    pane disappearing. While the job is non-terminal, the
			    refetch loop above keeps this live. */}
			<Card>
				<CardHeader>
					<CardTitle className="text-base">Logs</CardTitle>
				</CardHeader>
				<CardContent className="space-y-4">
					<LogStream label="stdout" content={job.stdout} terminal={isTerminal} />
					<LogStream label="stderr" content={job.stderr} terminal={isTerminal} />
				</CardContent>
			</Card>
		</div>
	)
}

// LogStream is one labelled log pane (stdout or stderr). When the
// job is non-terminal we show "streaming…" instead of the empty
// placeholder so the user knows we're still waiting for the BIN.
// When terminal + empty, we make explicit that the BIN produced
// nothing — distinguishing "no output" from "loading".
function LogStream({
	label,
	content,
	terminal,
}: {
	label: string
	content: string
	terminal: boolean
}) {
	let body: React.ReactNode
	if (content !== "") {
		body = (
			<pre className="overflow-x-auto whitespace-pre-wrap rounded bg-muted px-3 py-2 font-mono text-xs">
				{content}
			</pre>
		)
	} else if (terminal) {
		body = (
			<p className="rounded border border-dashed px-3 py-2 text-muted-foreground text-xs italic">
				(no {label})
			</p>
		)
	} else {
		body = (
			<p className="rounded border border-dashed px-3 py-2 text-muted-foreground text-xs italic">
				streaming {label}…
			</p>
		)
	}
	return (
		<div className="space-y-1.5">
			<p className="font-medium text-muted-foreground text-xs uppercase tracking-wide">
				{label}
			</p>
			{body}
		</div>
	)
}

function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<div className="grid grid-cols-[100px_1fr] items-start gap-2">
			<span className="text-muted-foreground text-xs">{label}</span>
			<div>{children}</div>
		</div>
	)
}

function formatUnix(unixSec: bigint): string {
	if (unixSec === 0n) return "-"
	return new Date(Number(unixSec) * 1000).toLocaleString()
}
