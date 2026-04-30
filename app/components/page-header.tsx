"use client"

import { Check, Copy, Terminal } from "lucide-react"
import type { ReactNode } from "react"
import { useState } from "react"
import { Button } from "@/components/ui/button"

interface PageHeaderProps {
	title: string
	description: ReactNode
	// CLI invocation that produces (roughly) the same data the page
	// shows. Rendered as a small copyable code chip beneath the
	// description so users can pick up the equivalent command.
	command?: string
	// Right-aligned slot for the page's primary action (Create / Add /
	// Build button or dialog trigger). Optional.
	actions?: ReactNode
}

export function PageHeader({ title, description, command, actions }: PageHeaderProps) {
	return (
		<div className="space-y-3">
			<div className="flex items-start justify-between gap-4">
				<div className="space-y-1">
					<h1 className="font-semibold text-2xl tracking-tight">{title}</h1>
					<p className="text-muted-foreground text-sm">{description}</p>
				</div>
				{actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
			</div>
			{command && <CommandChip command={command} />}
		</div>
	)
}

function CommandChip({ command }: { command: string }) {
	const [copied, setCopied] = useState(false)

	const handleCopy = async () => {
		try {
			await navigator.clipboard.writeText(command)
			setCopied(true)
			setTimeout(() => setCopied(false), 1500)
		} catch {
			// Older / hardened browsers without clipboard API — silently
			// no-op; the command is still selectable.
		}
	}

	return (
		<div className="inline-flex max-w-full items-center gap-2 rounded-md border bg-muted/50 py-1 pr-1 pl-3 font-mono text-xs">
			<Terminal className="h-3 w-3 shrink-0 text-muted-foreground" />
			<code className="truncate">{command}</code>
			<Button
				aria-label={copied ? "Copied" : "Copy command"}
				className="h-6 w-6 shrink-0"
				onClick={handleCopy}
				size="icon"
				variant="ghost">
				{copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
			</Button>
		</div>
	)
}
