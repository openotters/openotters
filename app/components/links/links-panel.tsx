"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { Link2, Link2Off } from "lucide-react"
import { useMemo, useState } from "react"
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
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select"
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table"
import type { LinkedAgent } from "@/lib/proto/v1/daemon_pb"
import {
	linkAgents,
	listAgentLinks,
	listAgents,
	unlinkAgents,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"

interface LinksPanelProps {
	agentRef: string
}

// LinksPanel renders the directed-graph view for one agent. Two
// tables: Outbound (agents this one can call) and Inbound (agents
// that can call this one). The operator can add/remove outbound
// links from this panel; inbound is read-only because the inbound
// side is owned by the other agent's outbound edge.
export function LinksPanel({ agentRef }: LinksPanelProps) {
	const queryClient = useQueryClient()
	const links = useQuery(listAgentLinks, { ref: agentRef })
	const allAgents = useQuery(listAgents, {})
	const [pendingTarget, setPendingTarget] = useState<string>("")

	const invalidate = () =>
		queryClient.invalidateQueries({
			queryKey: ["openotters.daemon.v1.Runtime", "ListAgentLinks"],
		})

	const link = useMutation(linkAgents, {
		onSuccess: (resp, vars) => {
			toast.success(
				resp.restarted
					? `Linked ${vars.sourceRef} → ${vars.targetRef} (source restarted)`
					: `Linked ${vars.sourceRef} → ${vars.targetRef}`,
			)
			setPendingTarget("")
			invalidate()
		},
		onError: (err) => toast.error(err.message),
	})

	const unlink = useMutation(unlinkAgents, {
		onSuccess: (resp, vars) => {
			toast.success(
				resp.restarted
					? `Unlinked ${vars.sourceRef} → ${vars.targetRef} (source restarted)`
					: `Unlinked ${vars.sourceRef} → ${vars.targetRef}`,
			)
			invalidate()
		},
		onError: (err) => toast.error(err.message),
	})

	const outbound = links.data?.outbound ?? []
	const inbound = links.data?.inbound ?? []

	// Build the "available to link" list: every agent that isn't
	// already linked outbound + isn't this agent itself.
	const linkableTargets = useMemo(() => {
		const outboundNames = new Set(outbound.map((a) => a.name))
		return (allAgents.data?.agents ?? []).filter(
			(a) => a.name !== agentRef && !outboundNames.has(a.name),
		)
	}, [outbound, allAgents.data, agentRef])

	return (
		<div className="space-y-4">
			<Card>
				<CardHeader>
					<div className="flex items-center justify-between">
						<div>
							<CardTitle>Outbound links</CardTitle>
							<CardDescription>
								Agents <code className="font-mono text-xs">{agentRef}</code> can call via{" "}
								<code>agent_chat</code> / <code>agent_exec</code> / <code>agent_info</code>.
								Adding or removing a link triggers an automatic restart so the JWT picks up
								the change.
							</CardDescription>
						</div>
						<div className="flex items-center gap-2">
							<Select
								disabled={linkableTargets.length === 0 || link.isPending}
								onValueChange={setPendingTarget}
								value={pendingTarget}
							>
								<SelectTrigger className="w-[220px]">
									<SelectValue placeholder="Link to…" />
								</SelectTrigger>
								<SelectContent>
									{linkableTargets.map((a) => (
										<SelectItem key={a.id} value={a.name}>
											{a.name}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
							<Button
								disabled={pendingTarget === "" || link.isPending}
								onClick={() =>
									link.mutate({ sourceRef: agentRef, targetRef: pendingTarget })
								}
								size="sm"
							>
								<Link2 className="mr-1 h-4 w-4" />
								Add link
							</Button>
						</div>
					</div>
				</CardHeader>
				<CardContent>
					<LinkedAgentsTable
						agents={outbound}
						emptyMessage="No outbound links — this agent can't call any others."
						onUnlink={(target) =>
							unlink.mutate({ sourceRef: agentRef, targetRef: target.name })
						}
					/>
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle>Inbound links</CardTitle>
					<CardDescription>
						Agents that can call <code className="font-mono text-xs">{agentRef}</code>. Read-
						only here — manage from the source agent's Links tab.
					</CardDescription>
				</CardHeader>
				<CardContent>
					<LinkedAgentsTable
						agents={inbound}
						emptyMessage="No inbound links — no agent has this one as a target."
					/>
				</CardContent>
			</Card>
		</div>
	)
}

interface LinkedAgentsTableProps {
	agents: LinkedAgent[]
	emptyMessage: string
	onUnlink?: (target: LinkedAgent) => void
}

function LinkedAgentsTable({ agents, emptyMessage, onUnlink }: LinkedAgentsTableProps) {
	if (agents.length === 0) {
		return <p className="py-4 text-center text-muted-foreground text-sm">{emptyMessage}</p>
	}

	return (
		<Table>
			<TableHeader>
				<TableRow>
					<TableHead>Name</TableHead>
					<TableHead>Model</TableHead>
					<TableHead className="w-[120px]">Status</TableHead>
					{onUnlink && <TableHead className="w-[100px]" />}
				</TableRow>
			</TableHeader>
			<TableBody>
				{agents.map((a) => (
					<TableRow key={a.id}>
						<TableCell className="font-mono text-sm">{a.name}</TableCell>
						<TableCell className="font-mono text-xs text-muted-foreground">{a.model}</TableCell>
						<TableCell>
							<Badge variant={statusVariant(a.status)}>{a.status || "—"}</Badge>
						</TableCell>
						{onUnlink && (
							<TableCell>
								<Button onClick={() => onUnlink(a)} size="sm" variant="ghost">
									<Link2Off className="mr-1 h-3 w-3" />
									Unlink
								</Button>
							</TableCell>
						)}
					</TableRow>
				))}
			</TableBody>
		</Table>
	)
}

function statusVariant(status: string): "default" | "secondary" | "destructive" {
	switch (status) {
		case "ready":
			return "default"
		case "failed":
		case "removed":
			return "destructive"
		default:
			return "secondary"
	}
}
