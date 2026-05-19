"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { KeyRound, Minus, Plus, RotateCcw, Save, ShieldAlert } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { toast } from "sonner"
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
	getAgentIdentity,
	listCapabilities,
	setAgentCapabilities,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"

interface CapabilitiesPanelProps {
	agentRef: string
}

// CapabilitiesPanel — inline-edit Capabilities surface. Lists
// every catalogue entry as a card; the operator toggles individual
// caps with the per-card +/− affordance and commits the whole
// batch via Save. No modal — the diff stays visible in the panel
// until Save (one restart) or Reset (discard) lands.
//
// Card states:
//   granted (in claim) + in draft ............ solid (will stay)
//   granted (in claim) + NOT in draft ......... strikethrough (will be removed on save)
//   NOT granted        + in draft ............. ghost / dashed border (will be added on save)
//   NOT granted        + NOT in draft ......... muted (available, untouched)
//
// On Save: calls SetAgentCapabilities with the draft set. Daemon
// validates names, persists, re-renders agent.yaml + AGENT.md,
// re-issues JWT, restarts the runtime once.
export function CapabilitiesPanel({ agentRef }: CapabilitiesPanelProps) {
	const queryClient = useQueryClient()
	const identity = useQuery(getAgentIdentity, { ref: agentRef })
	const catalogue = useQuery(listCapabilities, {})

	const granted = useMemo(
		() => identity.data?.claims?.capabilities ?? [],
		[identity.data?.claims?.capabilities],
	)
	const grantedSet = useMemo(() => new Set(granted), [granted])

	const entries = useMemo(
		() => catalogue.data?.capabilities ?? [],
		[catalogue.data?.capabilities],
	)
	const entriesByName = useMemo(() => {
		const m = new Map<string, string>()
		for (const e of entries) {
			m.set(e.name, e.description)
		}
		return m
	}, [entries])

	// `draft` is the operator's tentative cap set. Initialised to
	// the granted set; mutates as +/− buttons toggle.
	const [draft, setDraft] = useState<Set<string>>(grantedSet)
	const [query, setQuery] = useState("")

	// Re-sync the draft whenever the granted set comes back from a
	// refetch (e.g. after Save's invalidate, or another tab edited
	// caps). Keeps the panel in sync with the server.
	useEffect(() => {
		setDraft(new Set(granted))
	}, [granted])

	const save = useMutation(setAgentCapabilities, {
		onSuccess: (resp) => {
			queryClient.invalidateQueries({
				queryKey: ["openotters.daemon.v1.Runtime", "GetAgentIdentity"],
			})
			queryClient.invalidateQueries({
				queryKey: ["openotters.daemon.v1.Runtime", "ListAgents"],
			})
			toast.success(
				resp.restarted
					? "Capabilities saved — runtime restarted"
					: "Capabilities saved (agent stopped; effective next start)",
			)
		},
		onError: (err) => {
			toast.error("Save failed", { description: err.message })
		},
	})

	const toAdd = useMemo(
		() => [...draft].filter((n) => !grantedSet.has(n)),
		[draft, grantedSet],
	)
	const toRemove = useMemo(
		() => granted.filter((n) => !draft.has(n)),
		[granted, draft],
	)
	const dirty = toAdd.length > 0 || toRemove.length > 0

	if (identity.isLoading || catalogue.isLoading) {
		return <p className="text-muted-foreground">Loading capabilities…</p>
	}
	if (identity.error) {
		return (
			<Card>
				<CardHeader>
					<CardTitle className="text-base">Capabilities</CardTitle>
					<CardDescription>
						Failed to read the agent's identity: {identity.error.message}
					</CardDescription>
				</CardHeader>
			</Card>
		)
	}

	// Sort: granted-in-claim first (alphabetical), then catalogue
	// remainder (alphabetical). Stable ordering across edits so a
	// card doesn't visually jump when you toggle it.
	const sortedEntries = [...entries].sort((a, b) => {
		const ag = grantedSet.has(a.name) ? 0 : 1
		const bg = grantedSet.has(b.name) ? 0 : 1
		return ag - bg || a.name.localeCompare(b.name)
	})

	const q = query.trim().toLowerCase()
	const filtered = q
		? sortedEntries.filter(
				(e) =>
					e.name.toLowerCase().includes(q) ||
					e.description.toLowerCase().includes(q),
			)
		: sortedEntries

	return (
		<div className="space-y-4">
			<Card>
				<CardHeader>
					<div className="flex flex-row items-start justify-between gap-4">
						<div>
							<CardTitle className="text-base">
								Capabilities ({granted.length})
							</CardTitle>
							<CardDescription>
								Runtime tool surface this agent can call. Toggle individual
								caps with the +/− affordance on each card and click Save to
								commit the batch — the runtime restarts once per save, no
								matter how many adds + removes you queued.
							</CardDescription>
						</div>
						<Input
							className="max-w-xs"
							onChange={(e) => setQuery(e.target.value)}
							placeholder="Filter caps…"
							type="search"
							value={query}
						/>
					</div>
				</CardHeader>
				<CardContent className="space-y-3">
					{filtered.length === 0 ? (
						<p className="py-4 text-center text-muted-foreground text-sm">
							{q ? "No caps match the filter." : "Catalogue is empty."}
						</p>
					) : (
						<div className="grid gap-2 sm:grid-cols-2">
							{filtered.map((c) => (
								<CapabilityRow
									description={c.description}
									granted={grantedSet.has(c.name)}
									inDraft={draft.has(c.name)}
									key={c.name}
									name={c.name}
									onToggle={() => {
										setDraft((prev) => {
											const next = new Set(prev)
											if (next.has(c.name)) {
												next.delete(c.name)
											} else {
												next.add(c.name)
											}
											return next
										})
									}}
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
										{toAdd.map((n) => (
											<code className="mr-1 font-mono text-xs" key={n}>
												{n}
											</code>
										))}
									</span>
								</p>
							)}
							{toRemove.length > 0 && (
								<p>
									<span className="font-medium text-destructive">
										−{toRemove.length}
									</span>{" "}
									<span className="text-muted-foreground">
										{toRemove.map((n) => (
											<code className="mr-1 font-mono text-xs line-through" key={n}>
												{n}
											</code>
										))}
									</span>
								</p>
							)}
							<p className="text-muted-foreground text-xs">
								Saving restarts the runtime so the new tool surface takes
								effect. In-flight sessions on this agent are interrupted.
							</p>
						</div>
					</div>
					<div className="flex shrink-0 items-center gap-2">
						<Button
							disabled={save.isPending}
							onClick={() => setDraft(new Set(granted))}
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
									capabilities: [...draft],
								})
							}
							size="sm">
							<Save className="mr-2 h-4 w-4" />
							{save.isPending ? "Saving…" : "Save"}
						</Button>
					</div>
				</div>
			)}
		</div>
	)
}

interface CapabilityRowProps {
	name: string
	description: string
	granted: boolean
	inDraft: boolean
	onToggle: () => void
}

// CapabilityRow renders one cap card. Visual state derives from
// the (granted, inDraft) pair; the operator's +/− toggle drives
// inDraft, never granted (granted only flips on a successful Save).
function CapabilityRow({
	name,
	description,
	granted,
	inDraft,
	onToggle,
}: CapabilityRowProps) {
	const pendingAdd = !granted && inDraft
	const pendingRemove = granted && !inDraft

	const variant = pendingAdd
		? "border-emerald-500/50 bg-emerald-500/5"
		: pendingRemove
			? "border-destructive/50 bg-destructive/5"
			: granted
				? "border-border"
				: "border-dashed bg-muted/20"

	return (
		<div
			className={`flex items-start gap-2 rounded-lg border p-3 transition-colors ${variant}`}>
			<KeyRound
				className={`mt-0.5 h-4 w-4 shrink-0 ${
					pendingRemove
						? "text-destructive"
						: pendingAdd
							? "text-emerald-600"
							: granted
								? "text-primary"
								: "text-muted-foreground"
				}`}
			/>
			<div className="min-w-0 flex-1">
				<div className="flex items-center gap-2">
					<code
						className={`break-all font-mono font-medium text-sm ${
							pendingRemove ? "line-through text-muted-foreground" : ""
						}`}>
						{name}
					</code>
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
				{description && (
					<p
						className={`mt-1 text-xs ${
							pendingRemove ? "text-muted-foreground/60" : "text-muted-foreground"
						}`}>
						{description}
					</p>
				)}
			</div>
			<Button
				aria-label={inDraft ? `Remove ${name}` : `Add ${name}`}
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
