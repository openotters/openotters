"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { Plus, Trash2 } from "lucide-react"
import { useRouter } from "next/navigation"
import { useState } from "react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
	DialogTrigger,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { listAgents, submitAsyncJob } from "@/lib/proto/v1/daemon-Runtime_connectquery"

interface RunJobDialogProps {
	// Pre-selected agent name. When provided the agent picker is
	// hidden — used by the agent detail page where the agent is
	// already in scope. When undefined the dialog renders a picker
	// listing every running agent.
	agentRef?: string
	// Optional trigger override. Defaults to a "Run Job" button.
	trigger?: React.ReactNode
}

// RunJobDialog opens a form that calls SubmitAsyncJob. On success
// it routes to /jobs/<id> so the user can watch the new job tick
// to done in the existing detail view's polling loop.
//
// The BIN field is a select sourced from the picked agent's
// declared tools — typing a name that isn't declared would fail
// at submit anyway (daemon's bin-validation), so we constrain to
// the known-good set up-front.
export function RunJobDialog({ agentRef, trigger }: RunJobDialogProps) {
	const router = useRouter()
	const queryClient = useQueryClient()
	const [open, setOpen] = useState(false)

	// Form state. Reset when the dialog closes so a re-open starts
	// clean — otherwise the user's last submit would haunt the form.
	const [agent, setAgent] = useState(agentRef ?? "")
	const [bin, setBin] = useState("")
	const [args, setArgs] = useState("")
	const [stdin, setStdin] = useState("")
	const [labels, setLabels] = useState<Array<{ k: string; v: string }>>([])

	const agents = useQuery(listAgents, {}, { enabled: open && !agentRef })

	// Selected agent's tools drive the BIN select. When the agent
	// list hasn't loaded yet (or no agent picked yet) we fall back
	// to a free-text input so the user can still type.
	const agentList = agents.data?.agents ?? []
	const selectedAgent = agentList.find((a) => a.name === agent || a.id === agent)
	const declaredBins = selectedAgent?.tools.map((t) => t.name) ?? []

	const submit = useMutation(submitAsyncJob, {
		onSuccess: (resp) => {
			queryClient.invalidateQueries({
				queryKey: ["openotters.daemon.v1.Runtime", "ListAsyncJobs"],
			})
			toast.success(`Submitted ${resp.jobId}`)
			setOpen(false)
			resetForm()
			router.push(`/jobs/${resp.jobId}`)
		},
		onError: (err) => {
			toast.error("Submit failed", { description: err.message })
		},
	})

	function resetForm() {
		setAgent(agentRef ?? "")
		setBin("")
		setArgs("")
		setStdin("")
		setLabels([])
	}

	function handleSubmit(e: React.FormEvent) {
		e.preventDefault()
		if (!agent || !bin) return

		// Args: one per line. Empty lines drop out so a trailing
		// blank in the textarea doesn't become an empty positional
		// arg — that would surprise the BIN ("missing argument").
		const argList = args
			.split("\n")
			.map((s) => s.trim())
			.filter((s) => s !== "")

		// Labels: drop entries where either key or value is empty.
		// Daemon would store them but they'd never match a filter,
		// so they're noise; better to silently drop than send.
		const labelMap: { [key: string]: string } = {}
		for (const { k, v } of labels) {
			if (k && v) labelMap[k] = v
		}

		submit.mutate({
			agentRef: agent,
			bin,
			args: argList,
			stdin,
			labels: labelMap,
		})
	}

	return (
		<Dialog
			onOpenChange={(o) => {
				setOpen(o)
				if (!o) resetForm()
			}}
			open={open}>
			<DialogTrigger asChild>
				{trigger ?? (
					<Button size="sm">
						<Plus className="mr-2 h-4 w-4" />
						Run Job
					</Button>
				)}
			</DialogTrigger>
			<DialogContent className="max-w-lg">
				<form onSubmit={handleSubmit}>
					<DialogHeader>
						<DialogTitle>Run a job</DialogTitle>
						<DialogDescription>
							Dispatch a BIN against the agent's spawn env. Equivalent CLI:
							<code className="ml-1 rounded bg-muted px-1.5 py-0.5 font-mono text-xs">
								otters jobs run …
							</code>
						</DialogDescription>
					</DialogHeader>

					<div className="space-y-4 py-4">
						{/* Agent picker — hidden when scoped from an agent page. */}
						{!agentRef && (
							<div className="space-y-1.5">
								<Label htmlFor="run-job-agent">Agent</Label>
								<Select onValueChange={setAgent} value={agent}>
									<SelectTrigger id="run-job-agent">
										<SelectValue placeholder="Pick an agent" />
									</SelectTrigger>
									<SelectContent>
										{agentList.map((a) => (
											<SelectItem key={a.id} value={a.name}>
												{a.name}{" "}
												<span className="text-muted-foreground text-xs">
													({a.tools.length} bins)
												</span>
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							</div>
						)}

						<div className="space-y-1.5">
							<Label htmlFor="run-job-bin">Bin</Label>
							{declaredBins.length > 0 ? (
								<Select onValueChange={setBin} value={bin}>
									<SelectTrigger id="run-job-bin">
										<SelectValue placeholder="Pick a BIN declared in the agent's Agentfile" />
									</SelectTrigger>
									<SelectContent>
										{declaredBins.map((name) => (
											<SelectItem key={name} value={name}>
												{name}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							) : (
								<Input
									className="font-mono text-xs"
									id="run-job-bin"
									onChange={(e) => setBin(e.target.value)}
									placeholder={
										agent
											? "Agent declares no BINs — typing here will fail at submit"
											: "Pick an agent first"
									}
									value={bin}
								/>
							)}
						</div>

						<div className="space-y-1.5">
							<Label htmlFor="run-job-args">Args (one per line)</Label>
							<Textarea
								className="min-h-[80px] font-mono text-xs"
								id="run-job-args"
								onChange={(e) => setArgs(e.target.value)}
								placeholder={"-c\necho hello"}
								value={args}
							/>
						</div>

						<div className="space-y-1.5">
							<Label htmlFor="run-job-stdin">Stdin (optional)</Label>
							<Textarea
								className="min-h-[60px] font-mono text-xs"
								id="run-job-stdin"
								onChange={(e) => setStdin(e.target.value)}
								placeholder="payload piped to the BIN's stdin"
								value={stdin}
							/>
						</div>

						<div className="space-y-1.5">
							<div className="flex items-center justify-between">
								<Label>Labels</Label>
								<Button
									onClick={() => setLabels((l) => [...l, { k: "", v: "" }])}
									size="sm"
									type="button"
									variant="ghost">
									<Plus className="mr-1 h-3 w-3" />
									Add
								</Button>
							</div>
							{labels.length === 0 && (
								<p className="text-muted-foreground text-xs">
									None. Reserved keys live under{" "}
									<code className="font-mono text-[10px]">io.openotters.*</code> — see the
									daemon proto for the standard set.
								</p>
							)}
							{labels.map((row, i) => (
								<div className="flex gap-2" key={`label-${i}`}>
									<Input
										className="font-mono text-xs"
										onChange={(e) =>
											setLabels((l) =>
												l.map((x, idx) => (idx === i ? { ...x, k: e.target.value } : x)),
											)
										}
										placeholder="key"
										value={row.k}
									/>
									<Input
										className="font-mono text-xs"
										onChange={(e) =>
											setLabels((l) =>
												l.map((x, idx) => (idx === i ? { ...x, v: e.target.value } : x)),
											)
										}
										placeholder="value"
										value={row.v}
									/>
									<Button
										onClick={() => setLabels((l) => l.filter((_, idx) => idx !== i))}
										size="icon"
										type="button"
										variant="ghost">
										<Trash2 className="h-4 w-4" />
									</Button>
								</div>
							))}
						</div>
					</div>

					<DialogFooter>
						<Button
							onClick={() => setOpen(false)}
							size="sm"
							type="button"
							variant="ghost">
							Cancel
						</Button>
						<Button disabled={!agent || !bin || submit.isPending} size="sm" type="submit">
							{submit.isPending ? "Submitting…" : "Run"}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	)
}
