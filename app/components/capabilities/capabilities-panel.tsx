"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { KeyRound, Plus, ShieldAlert } from "lucide-react"
import { useMemo, useState } from "react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card"
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
	DialogTrigger,
} from "@/components/ui/dialog"
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select"
import {
	addAgentCapability,
	getAgentIdentity,
	listCapabilities,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"

interface CapabilitiesPanelProps {
	agentRef: string
}

// CapabilitiesPanel renders the agent's effective cap set with
// per-cap descriptions resolved against the daemon's catalogue,
// plus an "Add capability" affordance for the operator. Granting a
// cap re-issues the JWT and bounces the runtime — the panel warns
// before submission and invalidates the identity query on success
// so the granted cap appears live without a manual refresh.
//
// Source of truth for *currently granted* caps is the JWT claim
// (GetAgentIdentity → claims.capabilities); the panel does not
// read agent.yaml directly. On a successful grant the daemon
// returns the post-grant set in the AddAgentCapabilityResponse —
// but we still invalidate the identity query rather than splicing
// the response, so the panel stays consistent with the canonical
// JWT view even on restart races.
export function CapabilitiesPanel({ agentRef }: CapabilitiesPanelProps) {
	const queryClient = useQueryClient()
	const identity = useQuery(getAgentIdentity, { ref: agentRef })
	const catalogue = useQuery(listCapabilities, {})
	const [open, setOpen] = useState(false)
	const [pick, setPick] = useState<string>("")

	const granted = identity.data?.claims?.capabilities ?? []
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

	const ungranted = useMemo(
		() => entries.filter((e) => !grantedSet.has(e.name)),
		[entries, grantedSet],
	)

	const add = useMutation(addAgentCapability, {
		onSuccess: (resp, vars) => {
			// Identity query owns the cap list — invalidate so the
			// granted entry renders without a hard refresh. The
			// runtime restart races a few hundred ms after this
			// response; the next ListAgents poll picks up "working"
			// → "ready" naturally.
			queryClient.invalidateQueries({
				queryKey: ["openotters.daemon.v1.Runtime", "GetAgentIdentity"],
			})
			queryClient.invalidateQueries({
				queryKey: ["openotters.daemon.v1.Runtime", "ListAgents"],
			})
			if (resp.added) {
				toast.success(
					resp.restarted
						? `Granted ${vars.capability} — runtime restarted`
						: `Granted ${vars.capability} (agent stopped; effective next start)`,
				)
			} else {
				toast.info(`${vars.capability} was already granted`)
			}
			setOpen(false)
			setPick("")
		},
		onError: (err, vars) => {
			toast.error(`Grant ${vars.capability} failed`, {
				description: err.message,
			})
		},
	})

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

	return (
		<div className="space-y-4">
			<Card>
				<CardHeader className="flex flex-row items-start justify-between gap-4">
					<div>
						<CardTitle className="text-base">
							Granted capabilities ({granted.length})
						</CardTitle>
						<CardDescription>
							The runtime tool surface this agent can call. Resolved from
							the JWT claim — both the runtime (tool registration) and
							the daemon (per-RPC gate) honour this list. Add one to
							restart the agent with the new tool live.
						</CardDescription>
					</div>
					<Dialog onOpenChange={setOpen} open={open}>
						<DialogTrigger asChild>
							<Button
								disabled={ungranted.length === 0}
								size="sm"
								variant="outline">
								<Plus className="mr-2 h-4 w-4" />
								Add capability
							</Button>
						</DialogTrigger>
						<DialogContent>
							<DialogHeader>
								<DialogTitle>Grant capability</DialogTitle>
								<DialogDescription>
									Pick a capability to grant{" "}
									<code className="font-mono text-xs">{agentRef}</code>. The
									daemon will re-issue the agent's JWT and restart its
									runtime so the new tool surface takes effect immediately.
									In-flight sessions on this agent are interrupted.
								</DialogDescription>
							</DialogHeader>
							<div className="space-y-3 py-2">
								<div className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/5 p-3 text-sm">
									<ShieldAlert className="mt-0.5 h-4 w-4 shrink-0 text-amber-600" />
									<p className="text-amber-900 dark:text-amber-100">
										Granting a capability is irreversible from the UI —
										revoke via{" "}
										<code className="font-mono text-xs">otters run</code>{" "}
										recreate with the desired{" "}
										<code className="font-mono text-xs">--cap</code> set.
									</p>
								</div>
								<Select onValueChange={setPick} value={pick}>
									<SelectTrigger>
										<SelectValue placeholder="Select capability…" />
									</SelectTrigger>
									<SelectContent>
										{ungranted.map((c) => (
											<SelectItem key={c.name} value={c.name}>
												<span className="font-mono">{c.name}</span>
											</SelectItem>
										))}
									</SelectContent>
								</Select>
								{pick !== "" && (
									<p className="text-muted-foreground text-xs">
										{entriesByName.get(pick)}
									</p>
								)}
							</div>
							<DialogFooter>
								<Button
									disabled={add.isPending}
									onClick={() => setOpen(false)}
									variant="outline">
									Cancel
								</Button>
								<Button
									disabled={pick === "" || add.isPending}
									onClick={() =>
										add.mutate({ ref: agentRef, capability: pick })
									}>
									{add.isPending ? "Granting…" : "Grant + restart"}
								</Button>
							</DialogFooter>
						</DialogContent>
					</Dialog>
				</CardHeader>
				<CardContent>
					{granted.length === 0 ? (
						<p className="text-muted-foreground text-sm">
							No capabilities granted. The agent has no daemon-callback
							tools — it can only call BIN-declared tools.
						</p>
					) : (
						<div className="space-y-2">
							{granted.map((name) => (
								<div className="rounded-lg border p-3" key={name}>
									<div className="flex items-center gap-2">
										<KeyRound className="h-4 w-4 text-primary" />
										<code className="font-mono font-medium text-sm">
											{name}
										</code>
									</div>
									{entriesByName.has(name) && (
										<p className="mt-1 pl-6 text-muted-foreground text-xs">
											{entriesByName.get(name)}
										</p>
									)}
								</div>
							))}
						</div>
					)}
				</CardContent>
			</Card>
		</div>
	)
}
