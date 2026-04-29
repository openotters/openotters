"use client"

import { Check, Copy } from "lucide-react"
import { useState } from "react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"

export interface CliStep {
	caption?: string
	command: string
}

interface CliInstructionsDialogProps {
	open: boolean
	onOpenChange: (open: boolean) => void
	title: string
	description: string
	intro?: React.ReactNode
	steps: CliStep[]
	footer?: React.ReactNode
}

function CodeBlock({ command }: { command: string }) {
	const [copied, setCopied] = useState(false)

	const handleCopy = async () => {
		await navigator.clipboard.writeText(command)
		setCopied(true)
		setTimeout(() => setCopied(false), 1500)
	}

	return (
		<div className="group relative min-w-0">
			<pre className="max-w-full overflow-x-auto rounded-lg bg-muted p-3 pr-12 font-mono text-xs leading-relaxed sm:text-sm">
				<code>{command}</code>
			</pre>
			<Button
				aria-label="Copy command"
				className="absolute top-2 right-2 h-7 w-7 bg-muted/80 backdrop-blur"
				onClick={handleCopy}
				size="icon"
				variant="ghost">
				{copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
			</Button>
		</div>
	)
}

export function CliInstructionsDialog({
	open,
	onOpenChange,
	title,
	description,
	intro,
	steps,
	footer,
}: CliInstructionsDialogProps) {
	return (
		<Dialog onOpenChange={onOpenChange} open={open}>
			<DialogContent className="max-w-2xl overflow-hidden">
				<DialogHeader>
					<DialogTitle>{title}</DialogTitle>
					<DialogDescription>{description}</DialogDescription>
				</DialogHeader>
				<div className="space-y-4">
					{intro && <div className="text-muted-foreground text-sm">{intro}</div>}
					<ol className="space-y-4">
						{steps.map((step, index) => (
							<li className="space-y-2" key={`${index}-${step.command}`}>
								{step.caption && (
									<p className="text-muted-foreground text-sm">
										<span className="mr-2 inline-flex size-5 items-center justify-center rounded-full bg-primary/10 text-primary text-xs">
											{index + 1}
										</span>
										{step.caption}
									</p>
								)}
								<CodeBlock command={step.command} />
							</li>
						))}
					</ol>
					{footer && <div className="text-muted-foreground text-xs">{footer}</div>}
				</div>
			</DialogContent>
		</Dialog>
	)
}
