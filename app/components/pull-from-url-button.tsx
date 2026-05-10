"use client"

import { useMutation } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { Download } from "lucide-react"
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
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { pullAgentImage } from "@/lib/proto/v1/daemon-Runtime_connectquery"

interface PullFromUrlButtonProps {
	// Free-text label so /images and /bins can use the same component
	// without coupling its UI copy to either artifact type. The daemon
	// pulls any OCI ref the same way — the artifactType filter on the
	// page decides what shows up afterwards.
	label?: string
	placeholder?: string
}

export function PullFromUrlButton({
	label = "Pull from URL",
	placeholder = "ghcr.io/openotters/tools/jq:latest",
}: PullFromUrlButtonProps) {
	const queryClient = useQueryClient()
	const [open, setOpen] = useState(false)
	const [ref, setRef] = useState("")

	const pull = useMutation(pullAgentImage, {
		onMutate: (vars) => ({
			toastId: toast.loading(`Pulling ${vars.ref}…`),
		}),
		onSuccess: (_data, vars, ctx) => {
			queryClient.invalidateQueries({
				queryKey: ["openotters.daemon.v1.Runtime", "ListImages"],
			})
			toast.success(`Pulled ${vars.ref}`, { id: ctx?.toastId })
			setOpen(false)
			setRef("")
		},
		onError: (err, vars, ctx) => {
			toast.error(`Pull failed: ${vars.ref}`, {
				description: err.message,
				id: ctx?.toastId,
			})
		},
	})

	const trimmed = ref.trim()
	const submit = () => {
		if (trimmed === "") return
		pull.mutate({ ref: trimmed })
	}

	return (
		<>
			<Button onClick={() => setOpen(true)} variant="outline">
				<Download className="mr-2 h-4 w-4" />
				{label}
			</Button>
			<Dialog onOpenChange={setOpen} open={open}>
				<DialogContent>
					<DialogHeader>
						<DialogTitle>{label}</DialogTitle>
						<DialogDescription>
							Pull an OCI artifact from a remote registry into the local store.
							Equivalent to <code className="font-mono text-xs">otters image pull &lt;ref&gt;</code>.
						</DialogDescription>
					</DialogHeader>
					<form
						className="space-y-4"
						onSubmit={(e) => {
							e.preventDefault()
							submit()
						}}>
						<div className="space-y-2">
							<Label htmlFor="pull-ref">Image ref</Label>
							<Input
								autoFocus
								id="pull-ref"
								onChange={(e) => setRef(e.target.value)}
								placeholder={placeholder}
								value={ref}
							/>
						</div>
						<DialogFooter>
							<Button onClick={() => setOpen(false)} type="button" variant="ghost">
								Cancel
							</Button>
							<Button disabled={pull.isPending || trimmed === ""} type="submit">
								<Download className={`mr-2 h-4 w-4 ${pull.isPending ? "animate-pulse" : ""}`} />
								Pull
							</Button>
						</DialogFooter>
					</form>
				</DialogContent>
			</Dialog>
		</>
	)
}
