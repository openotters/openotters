"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { Activity, ArrowLeft, Bot, ChevronRight, FileText, Folder, History, KeyRound, Link2, ListChecks, MessageSquare, Pause, Play, ScrollText, ShieldCheck, StickyNote, Terminal, Trash2, Variable } from "lucide-react"
import Link from "next/link"
import { notFound, useRouter } from "next/navigation"
import { useMemo, useState } from "react"
import { toast } from "sonner"
import { ConfirmDelete } from "@/components/confirm-delete"
import { StatusBadge } from "@/components/status-badge"
import { useRouteParams } from "@/lib/use-route-params"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Separator } from "@/components/ui/separator"
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/components/ui/accordion"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
	deleteSession,
	describeImage,
	getAgentIdentity,
	getAgentLogs,
	listAgentLinks,
	listAgentNotes,
	listAgents,
	listAsyncJobs,
	listSessions,
	removeAgent,
	startAgent,
	stopAgent,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"
import { CapabilitiesPanel } from "@/components/capabilities/capabilities-panel"
import { IdentityPanel } from "@/components/identity/identity-panel"
import { JobsTable } from "@/components/jobs/jobs-table"
import { RunJobDialog } from "@/components/jobs/run-job-dialog"
import { LinksPanel } from "@/components/links/links-panel"
import { NotesPanel } from "@/components/notes/notes-panel"

function createdAtDate(unixSec: bigint): Date {
	return new Date(Number(unixSec) * 1000)
}

export default function AgentDetailPage() {
	const params = useRouteParams<{ agent: string }>("/agents/:agent")
	const router = useRouter()
	const queryClient = useQueryClient()
	const agentName = params.agent ?? ""
	// Controlled tab state so the dashboard's stat cards can switch
	// the active tab on click. Default lands on the Dashboard.
	const [activeTab, setActiveTab] = useState("dashboard")

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
	// notes / links: cheap RPCs that drive the count badges on
	// their respective tabs. Refetch slowly — the operator rarely
	// adds notes/links from outside the agent page itself.
	const notes = useQuery(
		listAgentNotes,
		{ ref: agentName },
		{ enabled: agentName !== "", refetchInterval: 30_000 },
	)
	const links = useQuery(
		listAgentLinks,
		{ ref: agentName },
		{ enabled: agentName !== "", refetchInterval: 30_000 },
	)
	// Identity drives the cap-count badge on the Capabilities tab.
	// Same refetch tier as notes / links — caps only mutate via the
	// dashboard (or the Capabilities tab itself, which invalidates
	// this query on save), so a slow tick is fine.
	const identity = useQuery(
		getAgentIdentity,
		{ ref: agentName },
		{ enabled: agentName !== "", refetchInterval: 30_000 },
	)

	const agent0 = list.data?.agents.find((a) => a.name === agentName)
	// describeImage drives the Env + Contexts tabs' card lists; we
	// lift the query here so the tab badges can read the count
	// without each component running its own duplicate fetch.
	// TanStack dedups identical queries so AgentEnvCard /
	// AgentContextsCard re-use this result for free.
	const desc = useQuery(
		describeImage,
		{ ref: agent0?.image ?? "" },
		{ enabled: !!agent0?.image },
	)
	const imageCounts = useMemo(() => {
		if (!desc.data?.config) return { envs: 0, contexts: 0 }
		try {
			const spec = JSON.parse(desc.data.config) as {
				agent?: { envs?: unknown[]; contexts?: unknown[] }
			}
			return {
				envs: Array.isArray(spec.agent?.envs) ? spec.agent.envs.length : 0,
				contexts: Array.isArray(spec.agent?.contexts)
					? spec.agent.contexts.length
					: 0,
			}
		} catch {
			return { envs: 0, contexts: 0 }
		}
	}, [desc.data?.config])
	const agentForJobs = agent0
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

	// Agent is "alive" when ready or actively working — both gate the
	// same set of action buttons (chat enabled, stop available, …).
	const running = agent.status === "ready" || agent.status === "working"

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
							<StatusBadge status={agent.status} failureReason={agent.failureReason} />
						</div>
						<p className="font-mono text-muted-foreground text-sm">{agent.model}</p>
					</div>
				</div>
				<div className="flex items-center gap-2">
					<Button asChild size="sm" variant="outline">
						<Link href={`/agents/${agent.name}/chat`}>
							<MessageSquare className="mr-2 h-4 w-4" />
							New session
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

			<Tabs
				className="grid gap-6 lg:grid-cols-[220px_1fr]"
				onValueChange={setActiveTab}
				orientation="vertical"
				value={activeTab}>
				<TabsList className="flex h-auto flex-col items-stretch gap-1 bg-transparent p-1 lg:sticky lg:top-4 lg:self-start">
					<TabsTrigger className="justify-start gap-2" value="dashboard">
						<Activity className="h-4 w-4" />
						Dashboard
					</TabsTrigger>
					<TabsTrigger className="justify-start gap-2" value="bins">
						<Terminal className="h-4 w-4" />
						<span className="flex-1 text-left">Bins</span>
						{agent.tools.length > 0 && (
							<Badge className="ml-1" variant="secondary">
								{agent.tools.length}
							</Badge>
						)}
					</TabsTrigger>
					<TabsTrigger className="justify-start gap-2" value="mounts">
						<Folder className="h-4 w-4" />
						<span className="flex-1 text-left">Mounts</span>
						{agent.mounts.length > 0 && (
							<Badge className="ml-1" variant="secondary">
								{agent.mounts.length}
							</Badge>
						)}
					</TabsTrigger>
					<TabsTrigger className="justify-start gap-2" value="env">
						<Variable className="h-4 w-4" />
						<span className="flex-1 text-left">Env</span>
						{imageCounts.envs > 0 && (
							<Badge className="ml-1" variant="secondary">
								{imageCounts.envs}
							</Badge>
						)}
					</TabsTrigger>
					<TabsTrigger className="justify-start gap-2" value="contexts">
						<FileText className="h-4 w-4" />
						<span className="flex-1 text-left">Contexts</span>
						{imageCounts.contexts > 0 && (
							<Badge className="ml-1" variant="secondary">
								{imageCounts.contexts}
							</Badge>
						)}
					</TabsTrigger>
					<TabsTrigger className="justify-start gap-2" value="logs">
						<ScrollText className="h-4 w-4" />
						Logs
					</TabsTrigger>
					<TabsTrigger className="justify-start gap-2" value="sessions">
						<History className="h-4 w-4" />
						<span className="flex-1 text-left">Sessions</span>
						{sessions.data?.sessions && sessions.data.sessions.length > 0 && (
							<Badge className="ml-1" variant="secondary">
								{sessions.data.sessions.length}
							</Badge>
						)}
					</TabsTrigger>
					<TabsTrigger className="justify-start gap-2" value="jobs">
						<ListChecks className="h-4 w-4" />
						<span className="flex-1 text-left">Jobs</span>
						{jobs.data?.jobs && jobs.data.jobs.length > 0 && (
							<Badge className="ml-1" variant="secondary">
								{jobs.data.jobs.length}
							</Badge>
						)}
					</TabsTrigger>
					<TabsTrigger className="justify-start gap-2" value="notes">
						<StickyNote className="h-4 w-4" />
						<span className="flex-1 text-left">Notes</span>
						{notes.data?.notes && notes.data.notes.length > 0 && (
							<Badge className="ml-1" variant="secondary">
								{notes.data.notes.length}
							</Badge>
						)}
					</TabsTrigger>
					<TabsTrigger className="justify-start gap-2" value="links">
						<Link2 className="h-4 w-4" />
						<span className="flex-1 text-left">Links</span>
						{links.data &&
							links.data.outbound.length + links.data.inbound.length > 0 && (
								<Badge className="ml-1" variant="secondary">
									{links.data.outbound.length + links.data.inbound.length}
								</Badge>
							)}
					</TabsTrigger>
					<TabsTrigger className="justify-start gap-2" value="capabilities">
						<ShieldCheck className="h-4 w-4" />
						<span className="flex-1 text-left">Capabilities</span>
						{identity.data?.claims &&
							identity.data.claims.capabilities.length > 0 && (
								<Badge className="ml-1" variant="secondary">
									{identity.data.claims.capabilities.length}
								</Badge>
							)}
					</TabsTrigger>
					<TabsTrigger className="justify-start gap-2" value="identity">
						<KeyRound className="h-4 w-4" />
						Identity
					</TabsTrigger>
				</TabsList>

				<div className="space-y-4">
					<TabsContent className="mt-0 space-y-4" value="dashboard">
						{/* Dashboard keeps the Agent Info side panel — every
						    other section is single-column. */}
						<div className="grid gap-4 lg:grid-cols-[1fr_320px]">
							<div className="space-y-4">
								<AgentDashboard
									agent={agent}
									agentName={agentName}
									sessions={sessions.data?.sessions ?? []}
									jobs={jobs.data?.jobs ?? []}
									onJump={setActiveTab}
								/>
							</div>
							<div className="space-y-4">
								<Card>
									<CardHeader>
										<CardTitle className="text-base">Agent Info</CardTitle>
									</CardHeader>
									<CardContent className="space-y-3 text-sm">
										<Row label="ID" value={agent.id || "—"} />
										<Separator />
										<Row label="Name" mono value={agent.name} />
										<Separator />
										<Row label="Model" mono value={agent.model} />
										<Separator />
										<Row label="Image" mono truncate value={agent.image || "—"} />
										{agent.imageDigest && (
											<>
												<Separator />
												<Row
													label="Image digest"
													mono
													value={`${agent.imageDigest.substring(0, 19)}…`}
												/>
											</>
										)}
										<Separator />
										<Row label="Runtime" mono truncate value={agent.runtimeRef || "—"} />
										<Separator />
										<Row label="Addr" mono value={agent.addr || "—"} />
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
							</div>
						</div>
					</TabsContent>

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
										<p className="py-4 text-center text-muted-foreground text-sm">
											No bins configured.
										</p>
									) : (
										<div className="grid gap-2 sm:grid-cols-2">
											{agent.tools.map((tool) => (
												<div
													className="flex items-start gap-2 rounded-lg border p-3"
													key={tool.name}>
													<Terminal className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
													<div className="min-w-0 flex-1">
														<code className="break-all font-mono font-medium text-sm">
															{tool.name}
														</code>
														{tool.description && (
															<p className="mt-1 text-muted-foreground text-xs">
																{tool.description}
															</p>
														)}
														<p className="mt-1 break-all font-mono text-muted-foreground/80 text-[11px]">
															{tool.ref}
														</p>
														{tool.digest && (
															<p className="font-mono text-muted-foreground/60 text-[11px]">
																{tool.digest.substring(0, 19)}…
															</p>
														)}
													</div>
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
														<span>{mount.host}</span>
														<span className="text-muted-foreground">→</span>
														<span>{mount.target}</span>
														{mount.readOnly && (
															<span className="rounded bg-muted px-1.5 py-0.5 text-muted-foreground text-xs">
																ro
															</span>
														)}
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

						<TabsContent className="space-y-4 pt-4" value="env">
							<AgentEnvCard agentImage={agent.image} />
						</TabsContent>

						<TabsContent className="space-y-4 pt-4" value="contexts">
							<AgentContextsCard agentImage={agent.image} />
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

						<TabsContent className="space-y-4 pt-4" value="sessions">
							<Card>
								<CardHeader>
									<CardTitle>Sessions</CardTitle>
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

						<TabsContent className="space-y-4 pt-4" value="notes">
							<NotesPanel ref={agent.name} />
						</TabsContent>

						<TabsContent className="space-y-4 pt-4" value="links">
							<LinksPanel agentRef={agent.name} />
						</TabsContent>

						<TabsContent className="space-y-4 pt-4" value="capabilities">
							<CapabilitiesPanel agentRef={agent.name} />
						</TabsContent>

						<TabsContent className="space-y-4 pt-4" value="identity">
							<IdentityPanel agentRef={agent.name} />
						</TabsContent>
				</div>
			</Tabs>
		</div>
	)
}

// AgentEnvCard surfaces the environment variables the agent's image
// declares (the ENV directives baked into the Agentfile). Resolves
// via DescribeImage which returns the image config as a JSON spec
// blob — same path the `otters agent inspect` CLI uses. Runtime
// `-e KEY=VAL` overrides aren't on the AgentInfo proto, so they're
// not surfaced here; if/when the daemon exposes them we layer the
// override column on top.
function AgentEnvCard({ agentImage }: { agentImage: string }) {
	const enabled = agentImage !== ""
	const desc = useQuery(describeImage, { ref: agentImage }, { enabled })

	type ImageEnv = { key: string; value: string; description?: string }
	let envs: ImageEnv[] = []
	let parseError: string | null = null
	if (desc.data?.config) {
		try {
			const spec = JSON.parse(desc.data.config) as {
				agent?: { envs?: ImageEnv[] }
			}
			envs = spec.agent?.envs ?? []
		} catch (e) {
			parseError = e instanceof Error ? e.message : String(e)
		}
	}

	return (
		<Card>
			<CardHeader>
				<CardTitle>Environment</CardTitle>
				<CardDescription>
					Variables baked into the agent's image via{" "}
					<code className="font-mono text-xs">ENV</code> directives in the Agentfile. Run-time
					overrides passed via{" "}
					<code className="font-mono text-xs">otters run -e KEY=VAL</code> are layered on top
					at spawn but aren't reflected here yet.
				</CardDescription>
			</CardHeader>
			<CardContent>
				{!enabled && (
					<p className="text-muted-foreground text-sm">
						Agent has no image ref — no environment to display.
					</p>
				)}
				{enabled && desc.isLoading && (
					<p className="text-muted-foreground text-sm">Loading image config…</p>
				)}
				{desc.error && (
					<p className="text-destructive text-sm">
						Failed to describe image: {desc.error.message}
					</p>
				)}
				{parseError && (
					<p className="text-destructive text-sm">
						Failed to parse image config: {parseError}
					</p>
				)}
				{enabled && !desc.isLoading && !desc.error && envs.length === 0 && (
					<p className="text-muted-foreground text-sm">
						No environment variables declared.
					</p>
				)}
				{envs.length > 0 && (
					<div className="grid gap-2 sm:grid-cols-2">
						{envs.map((e) => (
							<div className="flex items-start gap-2 rounded-lg border p-3" key={e.key}>
								<Variable className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
								<div className="min-w-0 flex-1">
									<div className="flex items-baseline gap-1.5">
										<code className="break-all font-mono font-medium text-sm">
											{e.key}
										</code>
										<span className="text-muted-foreground text-xs">=</span>
										<code className="break-all font-mono text-muted-foreground text-xs">
											{e.value || '""'}
										</code>
									</div>
									{e.description && (
										<p className="mt-1 text-muted-foreground text-xs">{e.description}</p>
									)}
								</div>
							</div>
						))}
					</div>
				)}
			</CardContent>
		</Card>
	)
}

// AgentContextsCard surfaces the markdown context files baked into
// the agent's image (the CONTEXT directives from the Agentfile).
// Each one is loaded into the model's system prompt at run time;
// the operator usually has no way to see what's in there short of
// exec-ing into the running container. Resolves via DescribeImage
// against the agent's image ref — same pattern as AgentEnvCard.
function AgentContextsCard({ agentImage }: { agentImage: string }) {
	const enabled = agentImage !== ""
	const desc = useQuery(describeImage, { ref: agentImage }, { enabled })

	type ImageContext = { name: string; description?: string; content?: string }
	let contexts: ImageContext[] = []
	let parseError: string | null = null
	if (desc.data?.config) {
		try {
			const spec = JSON.parse(desc.data.config) as {
				agent?: { contexts?: ImageContext[] }
			}
			contexts = spec.agent?.contexts ?? []
		} catch (e) {
			parseError = e instanceof Error ? e.message : String(e)
		}
	}

	return (
		<Card>
			<CardHeader>
				<CardTitle>Contexts</CardTitle>
				<CardDescription>
					Markdown files baked into the agent's image at{" "}
					<code className="font-mono text-xs">/etc/context/</code> and loaded into the
					model's system prompt at run time. Click a name to see the body the model sees.
				</CardDescription>
			</CardHeader>
			<CardContent>
				{!enabled && (
					<p className="text-muted-foreground text-sm">
						Agent has no image ref — no contexts to display.
					</p>
				)}
				{enabled && desc.isLoading && (
					<p className="text-muted-foreground text-sm">Loading image config…</p>
				)}
				{desc.error && (
					<p className="text-destructive text-sm">
						Failed to describe image: {desc.error.message}
					</p>
				)}
				{parseError && (
					<p className="text-destructive text-sm">
						Failed to parse image config: {parseError}
					</p>
				)}
				{enabled && !desc.isLoading && !desc.error && contexts.length === 0 && (
					<p className="text-muted-foreground text-sm">
						No contexts declared on this agent.
					</p>
				)}
				{contexts.length > 0 && (
					<Accordion className="space-y-2" collapsible type="single">
						{contexts.map((ctx) => (
							<AccordionItem
								className="rounded-lg border data-[state=open]:bg-muted/30"
								key={ctx.name}
								value={ctx.name}>
								<AccordionTrigger className="px-3 py-2 hover:no-underline">
									<div className="flex min-w-0 flex-1 items-start gap-2 text-left">
										<FileText className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
										<div className="min-w-0 flex-1">
											<span className="break-all font-medium font-mono text-sm">
												{ctx.name}
											</span>
											{ctx.description && (
												<p className="mt-0.5 text-muted-foreground text-xs">
													{ctx.description}
												</p>
											)}
										</div>
									</div>
								</AccordionTrigger>
								<AccordionContent className="px-3 pb-3">
									{ctx.content ? (
										<pre className="overflow-x-auto whitespace-pre-wrap break-words rounded-md bg-background p-3 font-mono text-xs">
											{ctx.content}
										</pre>
									) : (
										<p className="text-muted-foreground text-sm italic">
											No content recorded for this context.
										</p>
									)}
								</AccordionContent>
							</AccordionItem>
						))}
					</Accordion>
				)}
			</CardContent>
		</Card>
	)
}

// AgentDashboardProps captures only the fields the dashboard reads
// — keeps the component decoupled from the larger Agent / Session /
// AsyncJob proto types and re-usable if we ever lift it into its
// own file. `onJump` switches the controlled Tabs state in the
// parent so the stat-card click affordance feels native; `agentName`
// is the URL slug used to build session-resume links.
interface AgentDashboardProps {
	agent: {
		status: string
		createdAt: bigint
		tools: ReadonlyArray<unknown>
		image: string
		model: string
	}
	agentName: string
	sessions: ReadonlyArray<{
		id: string
		messageCount: number
		lastActive: bigint
	}>
	jobs: ReadonlyArray<{
		id: string
		status: string
		bin: string
		createdAt: bigint
	}>
	onJump: (tab: string) => void
}

const JOB_STATUSES = ["running", "done", "error", "cancelled", "orphaned", "pending"] as const

// AgentDashboard renders the at-a-glance summary at the top of the
// agent page. Synthesises live data from the queries already in
// flight (sessions / jobs / agent metadata) — no new RPC traffic.
//
// Two rows of stat cards over a recent-activity strip. Kept inline
// in this file because every data source is already in scope; if
// the component grows it can be moved to components/agents/.
function AgentDashboard({ agent, agentName, sessions, jobs, onJump }: AgentDashboardProps) {
	const totalSessions = sessions.length
	const totalMessages = sessions.reduce((acc, s) => acc + s.messageCount, 0)
	const lastSessionActive = sessions.reduce(
		(latest, s) => (s.lastActive > latest ? s.lastActive : latest),
		0n,
	)

	const jobsByStatus = new Map<string, number>()
	for (const j of jobs) {
		jobsByStatus.set(j.status, (jobsByStatus.get(j.status) ?? 0) + 1)
	}

	const running = jobsByStatus.get("running") ?? 0
	const finishedOK = jobsByStatus.get("done") ?? 0
	const failed =
		(jobsByStatus.get("error") ?? 0) +
		(jobsByStatus.get("orphaned") ?? 0) +
		(jobsByStatus.get("cancelled") ?? 0)

	// Newest first; pre-sorted by daemon but we belt-and-braces it.
	const recentSessions = [...sessions]
		.sort((a, b) => Number(b.lastActive - a.lastActive))
		.slice(0, 5)
	const recentJobs = [...jobs]
		.sort((a, b) => Number(b.createdAt - a.createdAt))
		.slice(0, 5)

	return (
		<div className="space-y-4">
			<div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
				<StatCard
					label="Status"
					value={agent.status || "—"}
					hint={
						agent.createdAt > 0n
							? `created ${createdAtDate(agent.createdAt).toLocaleDateString()}`
							: undefined
					}
				/>
				<StatCard
					label="Sessions"
					value={totalSessions.toString()}
					hint={
						totalSessions === 0
							? "no chats yet"
							: `${totalMessages} message${totalMessages === 1 ? "" : "s"} · last ${lastSessionActive > 0n ? relativeFromUnix(lastSessionActive) : "—"}`
					}
					onClick={() => onJump("sessions")}
				/>
				<StatCard
					label="Async jobs"
					value={jobs.length.toString()}
					hint={
						jobs.length === 0
							? "none yet"
							: `${running} running · ${finishedOK} done · ${failed} failed`
					}
					onClick={() => onJump("jobs")}
				/>
				<StatCard
					label="Bins"
					value={agent.tools.length.toString()}
					hint={agent.tools.length === 0 ? "no tools" : "see Bins tab"}
					onClick={() => onJump("bins")}
				/>
			</div>

			{jobs.length > 0 && <JobStatusBar buckets={JOB_STATUSES.map((s) => ({
				status: s,
				count: jobsByStatus.get(s) ?? 0,
			}))} />}

			<div className="grid gap-4 md:grid-cols-2">
				<Card>
					<CardHeader>
						<CardTitle className="text-base">Recent sessions</CardTitle>
					</CardHeader>
					<CardContent>
						{recentSessions.length === 0 ? (
							<p className="py-2 text-muted-foreground text-sm">No sessions yet.</p>
						) : (
							<ul className="space-y-1 text-sm">
								{recentSessions.map((s) => (
									<li key={s.id}>
										<Link
											className="-mx-2 flex items-center justify-between gap-3 rounded px-2 py-1.5 hover:bg-muted/60"
											href={`/agents/${agentName}/chat/${encodeURIComponent(s.id)}`}>
											<span className="truncate font-mono text-xs">{s.id}</span>
											<span className="shrink-0 text-muted-foreground text-xs">
												{s.messageCount} msg · {relativeFromUnix(s.lastActive)}
											</span>
										</Link>
									</li>
								))}
							</ul>
						)}
					</CardContent>
				</Card>
				<Card>
					<CardHeader>
						<CardTitle className="text-base">Recent jobs</CardTitle>
					</CardHeader>
					<CardContent>
						{recentJobs.length === 0 ? (
							<p className="py-2 text-muted-foreground text-sm">No jobs yet.</p>
						) : (
							<ul className="space-y-2 text-sm">
								{recentJobs.map((j) => (
									<li className="flex items-center justify-between gap-3" key={j.id}>
										<Link
											className="truncate font-mono text-xs hover:underline"
											href={`/jobs/${j.id}`}>
											{j.id}
										</Link>
										<span className="flex shrink-0 items-center gap-2 text-muted-foreground text-xs">
											<span className="font-mono">{j.bin}</span>
											<StatusBadge status={j.status} />
										</span>
									</li>
								))}
							</ul>
						)}
					</CardContent>
				</Card>
			</div>
		</div>
	)
}

interface StatCardProps {
	label: string
	value: string
	hint?: string
	// onClick makes the whole card a button-shaped affordance — used
	// by the dashboard so a stat card click jumps to the matching
	// tab. Omitting it renders the card as a passive readout
	// (e.g. the Status card has nowhere to jump to).
	onClick?: () => void
}

function StatCard({ label, value, hint, onClick }: StatCardProps) {
	const body = (
		<CardContent className="space-y-1 pt-4">
			<p className="text-muted-foreground text-xs uppercase tracking-wide">{label}</p>
			<p className="font-mono font-semibold text-2xl">{value}</p>
			{hint && <p className="text-muted-foreground text-xs">{hint}</p>}
		</CardContent>
	)

	if (!onClick) {
		return <Card>{body}</Card>
	}

	// Button-shaped Card: hover ring + cursor + keyboard focus so the
	// affordance is discoverable without a separate "View" link.
	return (
		<Card
			aria-label={`open ${label}`}
			className="cursor-pointer text-left transition-colors hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
			onClick={onClick}
			onKeyDown={(e) => {
				if (e.key === "Enter" || e.key === " ") {
					e.preventDefault()
					onClick()
				}
			}}
			role="button"
			tabIndex={0}>
			{body}
		</Card>
	)
}

// JOB_STATUS_COLORS maps the daemon's job-status strings to Tailwind
// background classes for the stacked bar. Pending/running share an
// accent shade so the "in flight" bucket reads as one; failure-side
// statuses cluster on the warning/destructive end.
const JOB_STATUS_COLORS: Record<string, string> = {
	pending: "bg-muted",
	running: "bg-amber-500",
	done: "bg-emerald-500",
	error: "bg-destructive",
	cancelled: "bg-muted-foreground/40",
	orphaned: "bg-destructive/60",
}

interface JobStatusBarProps {
	buckets: ReadonlyArray<{ status: string; count: number }>
}

// JobStatusBar renders a stacked horizontal bar showing the
// proportion of jobs in each status. Hidden entirely when there are
// no jobs — the empty state is already covered by the "Async jobs"
// stat card's hint.
function JobStatusBar({ buckets }: JobStatusBarProps) {
	const total = buckets.reduce((acc, b) => acc + b.count, 0)
	if (total === 0) return null

	return (
		<div className="space-y-1.5">
			<div className="flex h-2 overflow-hidden rounded">
				{buckets.map((b) =>
					b.count > 0 ? (
						<div
							className={JOB_STATUS_COLORS[b.status] ?? "bg-muted"}
							key={b.status}
							style={{ width: `${(b.count / total) * 100}%` }}
							title={`${b.status}: ${b.count}`}
						/>
					) : null,
				)}
			</div>
			<div className="flex flex-wrap gap-x-3 gap-y-1 text-muted-foreground text-xs">
				{buckets
					.filter((b) => b.count > 0)
					.map((b) => (
						<span className="flex items-center gap-1" key={b.status}>
							<span
								className={`h-2 w-2 rounded-sm ${JOB_STATUS_COLORS[b.status] ?? "bg-muted"}`}
							/>
							{b.status} · {b.count}
						</span>
					))}
			</div>
		</div>
	)
}

// relativeFromUnix renders a "5m ago" / "3h ago" / "2d ago" string.
// Keeps the dashboard compact — full timestamps live in the
// per-resource pages.
function relativeFromUnix(unixSec: bigint): string {
	if (unixSec === 0n) return "—"
	const secs = Math.max(0, Math.floor(Date.now() / 1000 - Number(unixSec)))
	if (secs < 60) return `${secs}s ago`
	if (secs < 3600) return `${Math.floor(secs / 60)}m ago`
	if (secs < 86_400) return `${Math.floor(secs / 3600)}h ago`
	return `${Math.floor(secs / 86_400)}d ago`
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
