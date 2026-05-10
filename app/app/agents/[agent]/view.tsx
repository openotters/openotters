"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { ArrowLeft, Bot, ChevronRight, History, ListChecks, MessageSquare, Pause, Play, ScrollText, Terminal, Trash2 } from "lucide-react"
import Link from "next/link"
import { notFound, useRouter } from "next/navigation"
import { toast } from "sonner"
import { ConfirmDelete } from "@/components/confirm-delete"
import { StatusBadge } from "@/components/status-badge"
import { useRouteParams } from "@/lib/use-route-params"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Separator } from "@/components/ui/separator"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
	deleteSession,
	getAgentLogs,
	listAgents,
	listAsyncJobs,
	listSessions,
	removeAgent,
	startAgent,
	stopAgent,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"
import { JobsTable } from "@/components/jobs/jobs-table"
import { RunJobDialog } from "@/components/jobs/run-job-dialog"

function createdAtDate(unixSec: bigint): Date {
	return new Date(Number(unixSec) * 1000)
}

export default function AgentDetailPage() {
	const params = useRouteParams<{ agent: string }>("/agents/:agent")
	const router = useRouter()
	const queryClient = useQueryClient()
	const agentName = params.agent ?? ""

	const list = useQuery(listAgents, {}, { enabled: agentName !== "" })
	const logs = useQuery(
		getAgentLogs,
		{ ref: agentName, tailLines: 200n },
		{ refetchInterval: 5_000, enabled: agentName !== "" },
	)
	const sessions = useQuery(
		listSessions,
		{ ref: agentName },
		{ enabled: agentName !== "", refetchInterval: 10_000 },
	)

	const agentForJobs = list.data?.agents.find((a) => a.name === agentName)
	const jobs = useQuery(
		listAsyncJobs,
		{ agentId: agentForJobs?.id ?? "" },
		// Run jobs poll separately from agents — short tick keeps the
		// "Jobs" tab live while the rest of the page (mostly static
		// agent metadata) doesn't refetch every 2s.
		{ enabled: !!agentForJobs?.id, refetchInterval: 2_000 },
	)

	const invalidate = () =>
		queryClient.invalidateQueries({ queryKey: ["openotters.daemon.v1.Runtime", "ListAgents"] })
	const start = useMutation(startAgent, { onSuccess: invalidate })
	const stop = useMutation(stopAgent, { onSuccess: invalidate })
	const remove = useMutation(removeAgent, {
		onSuccess: (_data, vars) => {
			invalidate()
			toast.success(`Removed agent ${vars.ref}`)
			router.push("/agents")
		},
		onError: (err, vars) => {
			toast.error(`Remove failed: ${vars.ref}`, { description: err.message })
		},
	})
	const removeSession = useMutation(deleteSession, {
		onSuccess: (_data, vars) => {
			queryClient.invalidateQueries({
				queryKey: ["openotters.daemon.v1.Runtime", "ListSessions"],
			})
			toast.success(`Deleted session ${vars.sessionId}`)
		},
		onError: (err, vars) => {
			toast.error(`Delete failed: ${vars.sessionId}`, { description: err.message })
		},
	})

	if (agentName === "" || list.isLoading) {
		return <p className="text-muted-foreground">Loading agent…</p>
	}

	const agent = list.data?.agents.find((a) => a.name === agentName)
	if (list.data && !agent) {
		notFound()
	}
	if (!agent) {
		return null
	}

	const running = agent.status === "running"

	return (
		<div className="space-y-6">
			<div className="flex items-center gap-4">
				<Button asChild size="icon" variant="ghost">
					<Link href="/agents">
						<ArrowLeft className="h-4 w-4" />
					</Link>
				</Button>
				<div className="flex flex-1 items-center gap-3">
					<div className="flex h-12 w-12 items-center justify-center rounded-lg bg-primary/10">
						<Bot className="h-6 w-6 text-primary" />
					</div>
					<div>
						<div className="flex items-center gap-2">
							<h1 className="font-semibold text-2xl tracking-tight">{agent.name}</h1>
							<StatusBadge status={agent.status} />
						</div>
						<p className="font-mono text-muted-foreground text-sm">{agent.model}</p>
					</div>
				</div>
				<div className="flex items-center gap-2">
					<Button asChild size="sm" variant="outline">
						<Link href={`/agents/${agent.name}/chat`}>
							<MessageSquare className="mr-2 h-4 w-4" />
							Chat
						</Link>
					</Button>
					{running ? (
						<Button
							disabled={stop.isPending}
							onClick={() => stop.mutate({ ref: agent.name })}
							size="sm"
							variant="outline">
							<Pause className="mr-2 h-4 w-4" />
							Stop
						</Button>
					) : (
						<Button
							disabled={start.isPending}
							onClick={() => start.mutate({ ref: agent.name })}
							size="sm"
							variant="outline">
							<Play className="mr-2 h-4 w-4" />
							Start
						</Button>
					)}
					<ConfirmDelete
						description={
							<>
								This stops and removes agent{" "}
								<code className="font-mono text-xs">{agent.name}</code>. The image stays in
								the registry; only this instance is deleted.
							</>
						}
						onConfirm={() => remove.mutate({ ref: agent.name })}
						pending={remove.isPending}
						title="Delete agent?"
						trigger={(open) => (
							<Button
								className="text-destructive hover:text-destructive"
								disabled={remove.isPending}
								onClick={open}
								size="sm"
								variant="outline">
								<Trash2 className="mr-2 h-4 w-4" />
								Delete
							</Button>
						)}
					/>
				</div>
			</div>

			<div className="grid gap-6 lg:grid-cols-[1fr_360px]">
				<div className="space-y-6">
					<Tabs className="w-full" defaultValue="bins">
						<TabsList>
							<TabsTrigger className="gap-2" value="bins">
								<Terminal className="h-4 w-4" />
								Bins
							</TabsTrigger>
							<TabsTrigger className="gap-2" value="mounts">
								<Terminal className="h-4 w-4" />
								Mounts
							</TabsTrigger>
							<TabsTrigger className="gap-2" value="logs">
								<ScrollText className="h-4 w-4" />
								Logs
							</TabsTrigger>
							<TabsTrigger className="gap-2" value="history">
								<History className="h-4 w-4" />
								History
								{sessions.data?.sessions && sessions.data.sessions.length > 0 && (
									<Badge className="ml-1" variant="secondary">
										{sessions.data.sessions.length}
									</Badge>
								)}
							</TabsTrigger>
							<TabsTrigger className="gap-2" value="jobs">
								<ListChecks className="h-4 w-4" />
								Jobs
								{jobs.data?.jobs && jobs.data.jobs.length > 0 && (
									<Badge className="ml-1" variant="secondary">
										{jobs.data.jobs.length}
									</Badge>
								)}
							</TabsTrigger>
						</TabsList>

						<TabsContent className="space-y-4 pt-4" value="bins">
							<Card>
								<CardHeader>
									<CardTitle>Bins</CardTitle>
									<CardDescription>
										Tools resolved against the local registry — one row per BIN directive in
										the agent's Agentfile.
									</CardDescription>
								</CardHeader>
								<CardContent>
									{agent.tools.length === 0 ? (
										<p className="py-4 text-center text-muted-foreground">
											No bins configured.
										</p>
									) : (
										<div className="space-y-3">
											{agent.tools.map((tool) => (
												<div className="rounded-lg border p-3" key={tool.name}>
													<div className="flex items-center gap-2">
														<Terminal className="h-4 w-4 text-primary" />
														<span className="font-medium font-mono">{tool.name}</span>
													</div>
													{tool.description && (
														<p className="mt-1 text-muted-foreground text-sm">
															{tool.description}
														</p>
													)}
													<p className="mt-1 font-mono text-muted-foreground text-xs">
														{tool.ref}
													</p>
													{tool.digest && (
														<p className="font-mono text-muted-foreground/70 text-xs">
															{tool.digest.substring(0, 19)}…
														</p>
													)}
												</div>
											))}
										</div>
									)}
								</CardContent>
							</Card>
						</TabsContent>

						<TabsContent className="space-y-4 pt-4" value="mounts">
							<Card>
								<CardHeader>
									<CardTitle>Bind mounts</CardTitle>
									<CardDescription>
										Host paths exposed to the agent via{" "}
										<code className="font-mono text-xs">otters run -v</code>.
									</CardDescription>
								</CardHeader>
								<CardContent>
									{agent.mounts.length === 0 ? (
										<p className="py-4 text-center text-muted-foreground">
											No mounts configured.
										</p>
									) : (
										<div className="space-y-3">
											{agent.mounts.map((mount, idx) => (
												<div className="rounded-lg border p-3" key={idx}>
													<div className="flex items-center gap-2 font-mono text-sm">
														<span>{mount.target}</span>
														<span className="text-muted-foreground">←</span>
														<span>{mount.host}</span>
													</div>
													{mount.description && (
														<p className="mt-1 text-muted-foreground text-sm">
															{mount.description}
														</p>
													)}
												</div>
											))}
										</div>
									)}
								</CardContent>
							</Card>
						</TabsContent>

						<TabsContent className="space-y-4 pt-4" value="logs">
							<Card>
								<CardHeader>
									<CardTitle>Runtime logs</CardTitle>
									<CardDescription>
										Tail of the agent's runtime log file. Equivalent to{" "}
										<code className="font-mono text-xs">otters logs {agent.name}</code>.
									</CardDescription>
								</CardHeader>
								<CardContent>
									<ScrollArea className="h-[400px] rounded-lg border bg-muted/30">
										<pre className="whitespace-pre-wrap p-3 font-mono text-xs">
											{logs.isLoading
												? "Loading…"
												: logs.error
													? `Failed to fetch logs: ${logs.error.message}`
													: new TextDecoder().decode(logs.data?.content ?? new Uint8Array())}
										</pre>
									</ScrollArea>
								</CardContent>
							</Card>
						</TabsContent>

						<TabsContent className="space-y-4 pt-4" value="history">
							<Card>
								<CardHeader>
									<CardTitle>Chat history</CardTitle>
									<CardDescription>
										Past sessions persisted by the runtime. Click any session to resume the
										conversation.
									</CardDescription>
								</CardHeader>
								<CardContent>
									{sessions.isLoading && (
										<p className="text-muted-foreground text-sm">Loading sessions…</p>
									)}
									{sessions.error && (
										<p className="text-destructive text-sm">
											Failed to fetch sessions: {sessions.error.message}
										</p>
									)}
									{!sessions.isLoading &&
										!sessions.error &&
										(sessions.data?.sessions.length ?? 0) === 0 && (
											<p className="text-muted-foreground text-sm">
												No sessions yet. Start a chat to see entries here.
											</p>
										)}
									{sessions.data && sessions.data.sessions.length > 0 && (
										<ul className="divide-y rounded-md border">
											{[...sessions.data.sessions]
												.sort((a, b) => Number(b.lastActive - a.lastActive))
												.map((s) => (
													<li className="flex items-center gap-2 pr-2" key={s.id}>
														<Link
															className="flex flex-1 items-center gap-3 px-3 py-2 hover:bg-muted/50"
															href={`/agents/${agent.name}/chat/${encodeURIComponent(s.id)}`}>
															<MessageSquare className="h-4 w-4 shrink-0 text-muted-foreground" />
															<div className="min-w-0 flex-1">
																<p className="truncate font-mono text-sm">{s.id}</p>
																<p className="text-muted-foreground text-xs">
																	{s.messageCount} message
																	{s.messageCount === 1 ? "" : "s"} ·{" "}
																	{new Date(Number(s.lastActive) * 1000).toLocaleString("en-US", {
																		month: "short",
																		day: "numeric",
																		hour: "2-digit",
																		minute: "2-digit",
																	})}
																</p>
															</div>
															<ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />
														</Link>
														<ConfirmDelete
															description={
																<>
																	Delete session{" "}
																	<code className="font-mono text-xs">{s.id}</code>? The
																	conversation history will be lost.
																</>
															}
															onConfirm={() =>
																removeSession.mutate({
																	ref: agent.name,
																	sessionId: s.id,
																})
															}
															pending={
																removeSession.isPending &&
																removeSession.variables?.sessionId === s.id
															}
															title="Delete session?"
															trigger={(open) => (
																<Button
																	aria-label={`Delete session ${s.id}`}
																	className="text-muted-foreground hover:text-destructive"
																	disabled={
																		removeSession.isPending &&
																		removeSession.variables?.sessionId === s.id
																	}
																	onClick={open}
																	size="icon"
																	variant="ghost">
																	<Trash2 className="h-4 w-4" />
																</Button>
															)}
														/>
													</li>
												))}
										</ul>
									)}
								</CardContent>
							</Card>
						</TabsContent>

						<TabsContent className="space-y-4 pt-4" value="jobs">
							<Card>
								<CardHeader className="flex flex-row items-start justify-between gap-4">
									<div>
										<CardTitle>Jobs</CardTitle>
										<CardDescription>
											Async BIN jobs dispatched against this agent's spawn env. Polls
											every 2s — running jobs visibly tick to done.
										</CardDescription>
									</div>
									<RunJobDialog agentRef={agent.name} />
								</CardHeader>
								<CardContent>
									<JobsTable agentColumn={false} jobs={jobs.data?.jobs ?? []} />
								</CardContent>
							</Card>
						</TabsContent>
					</Tabs>
				</div>

				<div className="space-y-4">
					<Card>
						<CardHeader>
							<CardTitle className="text-base">Agent Info</CardTitle>
						</CardHeader>
						<CardContent className="space-y-3 text-sm">
							<Row label="ID" value={agent.id || "—"} />
							<Separator />
							<Row label="Name" value={agent.name} mono />
							<Separator />
							<Row label="Model" value={agent.model} mono />
							<Separator />
							<Row label="Image" value={agent.image || "—"} mono truncate />
							{agent.imageDigest && (
								<>
									<Separator />
									<Row label="Image digest" value={agent.imageDigest.substring(0, 19) + "…"} mono />
								</>
							)}
							<Separator />
							<Row label="Runtime" value={agent.runtimeRef || "—"} mono truncate />
							<Separator />
							<Row label="Addr" value={agent.addr || "—"} mono />
							<Separator />
							<Row
								label="Created"
								value={
									agent.createdAt > 0n
										? createdAtDate(agent.createdAt).toLocaleString()
										: "—"
								}
							/>
						</CardContent>
					</Card>

					{agent.tools.length > 0 && (
						<Card>
							<CardHeader>
								<CardTitle className="flex items-center gap-2 text-base">
									<Terminal className="h-4 w-4" />
									Bins ({agent.tools.length})
								</CardTitle>
							</CardHeader>
							<CardContent>
								<div className="flex flex-wrap gap-1">
									{agent.tools.map((tool) => (
										<Badge className="font-mono text-xs" key={tool.name} variant="secondary">
											{tool.name}
										</Badge>
									))}
								</div>
							</CardContent>
						</Card>
					)}
				</div>
			</div>
		</div>
	)
}

interface RowProps {
	label: string
	value: React.ReactNode
	mono?: boolean
	truncate?: boolean
}

function Row({ label, value, mono, truncate }: RowProps) {
	return (
		<div className="flex justify-between gap-4">
			<span className="shrink-0 text-muted-foreground">{label}</span>
			<span
				className={`text-right ${mono ? "font-mono text-xs" : ""} ${truncate ? "truncate max-w-[200px]" : ""}`}>
				{value}
			</span>
		</div>
	)
}
