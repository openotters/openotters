"use client"

import { Plus } from "lucide-react"
import { useState } from "react"
import { CliInstructionsDialog } from "@/components/cli-instructions-dialog"
import { Button } from "@/components/ui/button"

export function AddBinButton() {
	const [open, setOpen] = useState(false)

	return (
		<>
			<Button onClick={() => setOpen(true)}>
				<Plus className="mr-2 h-4 w-4" />
				Add Custom Binary
			</Button>
			<CliInstructionsDialog
				description="A Bin is an OCI artifact carrying static binaries. Build one with the otters CLI."
				footer={
					<>
						See <code className="font-mono">otters bin build --help</code> for multi-arch builds and
						platform selection.
					</>
				}
				intro={
					<>
						Use a <span className="font-medium text-foreground">Binary Image</span> to expose a CLI tool
						(jq, curl, your own script, …) to agents via the{" "}
						<code className="font-mono">BIN</code> directive in an Agentfile.
					</>
				}
				onOpenChange={setOpen}
				open={open}
				steps={[
					{
						caption: "Pull a published bin into the local registry",
						command: "otters bin pull ghcr.io/openotters/tools/jq:latest",
					},
					{
						caption: "Or build one from local binaries",
						command:
							"otters bin build \\\n  -t my-tool:latest \\\n  --platform linux/amd64=./bin/amd64 \\\n  --platform linux/arm64=./bin/arm64",
					},
					{
						caption: "Push it to a remote registry",
						command: "otters bin push ghcr.io/myorg/my-tool:latest",
					},
					{
						caption: "Reference it from an Agentfile",
						command: 'BIN my-tool ghcr.io/myorg/my-tool:latest "What this tool does"',
					},
				]}
				title="Add a Binary"
			/>
		</>
	)
}
