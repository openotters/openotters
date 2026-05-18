"use client"

import { Sparkles } from "lucide-react"
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card"

interface CapabilitiesPanelProps {
	capabilities: string[]
}

// CapabilitiesPanel lists the auto-injected runtime tools the agent
// exposes (note_save, agent_create, job_submit, …). These are the
// tools the LLM sees in addition to the agent's BIN directives —
// the BIN list lives on its own tab. The set is driven by the
// daemon's runtimeCapsForExtras and depends on whether a daemon
// callback URL + agent token are wired.
export function CapabilitiesPanel({ capabilities }: CapabilitiesPanelProps) {
	if (capabilities.length === 0) {
		return (
			<Card>
				<CardHeader>
					<CardTitle className="text-base">Capabilities</CardTitle>
					<CardDescription>
						No runtime tools exposed for this agent.
					</CardDescription>
				</CardHeader>
			</Card>
		)
	}
	return (
		<Card>
			<CardHeader>
				<CardTitle className="text-base">Capabilities</CardTitle>
				<CardDescription>
					Auto-injected tools the LLM can call alongside the agent's BIN
					directives. The set is daemon-side — defined in pool.go and gated
					on the daemon callback being available.
				</CardDescription>
			</CardHeader>
			<CardContent>
				<div className="grid gap-2 sm:grid-cols-2">
					{capabilities.map((name) => (
						<div
							className="flex items-center gap-2 rounded-lg border p-2 font-mono text-sm"
							key={name}>
							<Sparkles className="h-4 w-4 shrink-0 text-primary" />
							<span className="break-all">{name}</span>
						</div>
					))}
				</div>
			</CardContent>
		</Card>
	)
}
