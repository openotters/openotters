"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { ArrowRight, Link2, Minus, Plus, RotateCcw, Save, ShieldAlert } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import {
	listAgentLinks,
	listAgents,
	setAgentLinks,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"

interface LinksPanelProps {
	agentRef: string
}

// LinksPanel — inline-edit outbound link surface. Lists every
// agent in the daemon as a card; the operator toggles individual
// targets with the per-card +/− affordance and commits the whole
// batch via Save. Inbound links stay read-only — they're owned by
// the other agent's outbound edge, not this one.
//
// Card states mirror the Capabilities panel:
//   linked (in claim) + in draft ............. solid (will stay)
//   linked (in claim) + NOT in draft .......... strikethrough (pending remove)
//   NOT linked        + in draft .............. ghost / dashed border (pending add)
//   NOT linked        + NOT in draft .......... muted (available)
//
// On Save: SetAgentLinks replaces the source agent's outbound link
// set in one shot. Daemon validates targets, applies the diff,
// re-issues the JWT, and restarts the runtime once.
export function LinksPanel({ agentRef }: LinksPanelProps) {
	const queryClient = useQueryClient()
	const links = useQuery(listAgentLinks, { ref: agentRef })
	const allAgents = useQuery(listAgents, {})

	const outbound = useMemo(() => links.data?.outbound ?? [], [links.data])
	const inbound = useMemo(() => links.data?.inbound ?? [], [links.data])
	const linkedSet = useMemo(
		() => new Set(outbound.map((l) => l.id)),
		[outbound],
	)

	// Pickable agent universe = every agent in the daemon EXCEPT
	// the source itself (self-link is rejected server-side). Show
	// the source's id so the filter pivots off id, not name.
	const sourceID = useMemo(() => {
		const self = allAgents.data?.agents.find((a) => a.name === agentRef)
		return self?.id ?? ""
	}, [allAgents.data, agentRef])

	const picks = useMemo(
		() =>
			(allAgents.data?.agents ?? []).filter((a) => a.id !== sourceID),
		[allAgents.data, sourceID],
	)

	const [draft, setDraft] = useState<Set<string>>(linkedSet)
	const [query, setQuery] = useState("")

	// Sync draft when the granted set refetches (post-save invalidate
	// or another tab edited links). Driven by linkedSet identity —
	// the memoised dep only changes when outbound itself does.
	useEffect(() => {
		setDraft(new Set(linkedSet))
	}, [linkedSet])

	const save = useMutation(setAgentLinks, {
		onSuccess: (resp) => {
			queryClient.invalidateQueries({
				queryKey: ["openotters.daemon.v1.Runtime", "ListAgentLinks"],
			})
			queryClient.invalidateQueries({
				queryKey: ["openotters.daemon.v1.Runtime", "ListAgents"],
			})
			toast.success(
				resp.restarted
					? "Links saved — source restarted"
					: "Links saved (source stopped; effective next start)",
			)
		},
		onError: (err) => {
			toast.error("Save failed", { description: err.message })
		},
	})

	const toAdd = useMemo(
		() => [...draft].filter((id) => !linkedSet.has(id)),
		[draft, linkedSet],
	)
	const toRemove = useMemo(
		() => outbound.filter((l) => !draft.has(l.id)),
		[outbound, draft],
	)
	const dirty = toAdd.length > 0 || toRemove.length > 0

	// Sort: linked-now first (alphabetical by name), then the
	// remainder. Stable across edits.
	const sortedPicks = [...picks].sort((a, b) => {
		const al = linkedSet.has(a.id) ? 0 : 1
		const bl = linkedSet.has(b.id) ? 0 : 1
		return al - bl || a.name.localeCompare(b.name)
	})

	const q = query.trim().toLowerCase()
	const filteredPicks = q
		? sortedPicks.filter(
				(a) =>
					a.name.toLowerCase().includes(q) ||
					(a.model ?? "").toLowerCase().includes(q),
			)
		: sortedPicks

	if (links.isLoading || allAgents.isLoading) {
		return <p className="text-muted-foreground">Loading links…</p>
	}

	return (
		<div className="space-y-4">
			<Card>
				<CardHeader>
					<div className="flex flex-row items-start justify-between gap-4">
						<div>
							<CardTitle className="text-base">
								Outbound links ({outbound.length})
							</CardTitle>
							<CardDescription>
								Agents this one can call via agent_exec / agent_info. Toggle
								targets with +/− and click Save to commit the batch — one
								restart per save.
							</CardDescription>
						</div>
						<Input
							className="max-w-xs"
							onChange={(e) => setQuery(e.target.value)}
							placeholder="Filter agents…"
							type="search"
							value={query}
						/>
					</div>
				</CardHeader>
				<CardContent>
					{filteredPicks.length === 0 ? (
						<p className="py-4 text-center text-muted-foreground text-sm">
							{q
								? "No agents match the filter."
								: "No other agents in the daemon to link to."}
						</p>
					) : (
						<div className="grid gap-2 sm:grid-cols-2">
							{filteredPicks.map((a) => (
								<LinkRow
									inDraft={draft.has(a.id)}
									key={a.id}
									linked={linkedSet.has(a.id)}
									model={a.model}
									name={a.name}
									onToggle={() => {
										setDraft((prev) => {
											const next = new Set(prev)
											if (next.has(a.id)) {
												next.delete(a.id)
											} else {
												next.add(a.id)
											}
											return next
										})
									}}
									status={a.status}
								/>
							))}
						</div>
					)}
				</CardContent>
			</Card>

			{dirty && (
				<div className="sticky bottom-4 z-10 flex flex-col gap-3 rounded-lg border border-amber-500/40 bg-amber-500/5 p-3 shadow-sm sm:flex-row sm:items-center sm:justify-between">
					<div className="flex items-start gap-2 text-sm">
						<ShieldAlert className="mt-0.5 h-4 w-4 shrink-0 text-amber-600" />
						<div className="space-y-1">
							{toAdd.length > 0 && (
								<p>
									<span className="font-medium text-emerald-700 dark:text-emerald-400">
										+{toAdd.length}
									</span>{" "}
									<span className="text-muted-foreground">
										{toAdd.map((id) => {
											const a = picks.find((p) => p.id === id)
											return (
												<code className="mr-1 font-mono text-xs" key={id}>
													{a?.name ?? id}
												</code>
											)
										})}
									</span>
								</p>
							)}
							{toRemove.length > 0 && (
								<p>
									<span className="font-medium text-destructive">
										−{toRemove.length}
									</span>{" "}
									<span className="text-muted-foreground">
										{toRemove.map((l) => (
											<code className="mr-1 font-mono text-xs line-through" key={l.id}>
												{l.name}
											</code>
										))}
									</span>
								</p>
							)}
							<p className="text-muted-foreground text-xs">
								Saving restarts the source so the new JWT.Links claim takes
								effect. In-flight sessions on this agent are interrupted.
							</p>
						</div>
					</div>
					<div className="flex shrink-0 items-center gap-2">
						<Button
							disabled={save.isPending}
							onClick={() => setDraft(new Set(linkedSet))}
							size="sm"
							variant="outline">
							<RotateCcw className="mr-2 h-4 w-4" />
							Reset
						</Button>
						<Button
							disabled={save.isPending}
							onClick={() =>
								save.mutate({
									ref: agentRef,
									targets: [...draft],
								})
							}
							size="sm">
							<Save className="mr-2 h-4 w-4" />
							{save.isPending ? "Saving…" : "Save"}
						</Button>
					</div>
				</div>
			)}

			<Card>
				<CardHeader>
					<CardTitle className="text-base">
						Inbound links ({inbound.length})
					</CardTitle>
					<CardDescription>
						Agents that can call this one. Read-only here — these edges are
						owned by the other agent's outbound configuration.
					</CardDescription>
				</CardHeader>
				<CardContent>
					{inbound.length === 0 ? (
						<p className="py-2 text-center text-muted-foreground text-sm">
							Nothing inbound. No other agent has{" "}
							<code className="font-mono text-xs">{agentRef}</code> on its
							outbound link list.
						</p>
					) : (
						<div className="grid gap-2 sm:grid-cols-2">
							{inbound.map((l) => (
								<InboundRow
									description={l.description}
									key={l.id}
									model={l.model}
									name={l.name}
									status={l.status}
								/>
							))}
						</div>
					)}
				</CardContent>
			</Card>
		</div>
	)
}

interface LinkRowProps {
	name: string
	model: string
	status: string
	linked: boolean
	inDraft: boolean
	onToggle: () => void
}

function LinkRow({
	name,
	model,
	status,
	linked,
	inDraft,
	onToggle,
}: LinkRowProps) {
	const pendingAdd = !linked && inDraft
	const pendingRemove = linked && !inDraft

	const variant = pendingAdd
		? "border-emerald-500/50 bg-emerald-500/5"
		: pendingRemove
			? "border-destructive/50 bg-destructive/5"
			: linked
				? "border-border"
				: "border-dashed bg-muted/20"

	return (
		<div
			className={`flex items-start gap-2 rounded-lg border p-3 transition-colors ${variant}`}>
			<Link2
				className={`mt-0.5 h-4 w-4 shrink-0 ${
					pendingRemove
						? "text-destructive"
						: pendingAdd
							? "text-emerald-600"
							: linked
								? "text-primary"
								: "text-muted-foreground"
				}`}
			/>
			<div className="min-w-0 flex-1">
				<div className="flex items-center gap-2">
					<span
						className={`break-all font-medium font-mono text-sm ${
							pendingRemove ? "line-through text-muted-foreground" : ""
						}`}>
						{name}
					</span>
					{pendingAdd && (
						<span className="rounded-full bg-emerald-500/10 px-1.5 py-0.5 font-medium text-[10px] text-emerald-700 uppercase tracking-wide dark:text-emerald-400">
							pending add
						</span>
					)}
					{pendingRemove && (
						<span className="rounded-full bg-destructive/10 px-1.5 py-0.5 font-medium text-[10px] text-destructive uppercase tracking-wide">
							pending remove
						</span>
					)}
				</div>
				<p className="mt-0.5 flex items-center gap-1.5 text-muted-foreground text-xs">
					<code className="font-mono">{model || "—"}</code>
					<Badge className="px-1 py-0 text-[10px]" variant="secondary">
						{status}
					</Badge>
				</p>
			</div>
			<Button
				aria-label={inDraft ? `Unlink ${name}` : `Link ${name}`}
				className="shrink-0"
				onClick={onToggle}
				size="icon"
				variant={inDraft ? "ghost" : "outline"}>
				{inDraft ? (
					<Minus className="h-4 w-4" />
				) : (
					<Plus className="h-4 w-4" />
				)}
			</Button>
		</div>
	)
}

interface InboundRowProps {
	name: string
	model: string
	status: string
	description: string
}

function InboundRow({ name, model, status, description }: InboundRowProps) {
	return (
		<div className="flex items-start gap-2 rounded-lg border p-3">
			<ArrowRight className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
			<div className="min-w-0 flex-1">
				<div className="flex items-center gap-2">
					<span className="break-all font-medium font-mono text-sm">{name}</span>
				</div>
				<p className="mt-0.5 flex items-center gap-1.5 text-muted-foreground text-xs">
					<code className="font-mono">{model || "—"}</code>
					<Badge className="px-1 py-0 text-[10px]" variant="secondary">
						{status}
					</Badge>
				</p>
				{description && (
					<p className="mt-1 text-muted-foreground text-xs">{description}</p>
				)}
			</div>
		</div>
	)
}
