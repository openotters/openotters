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
				Build Binary
			</Button>
			<CliInstructionsDialog
				description="Build a binary image from a local executable using the otters CLI."
				footer={
					<>
						See <code className="font-mono">otters bin build --help</code> for multi-arch builds and
						platform selection.
					</>
				}
				intro={
					<>
						A <span className="font-medium text-foreground">Binary Image</span> is an OCI artifact
						carrying a static binary. Reference one from an Agentfile via the{" "}
						<code className="font-mono">BIN</code> directive.
					</>
				}
				onOpenChange={setOpen}
				open={open}
				steps={[
					{
						caption: "Build a single-arch image from a local binary",
						command: "otters bin build -t my-tool:latest --platform linux/amd64=./my-tool",
					},
					{
						caption: "Or build a multi-arch image",
						command:
							"otters bin build \\\n  -t my-tool:latest \\\n  --platform linux/amd64=./bin/amd64 \\\n  --platform linux/arm64=./bin/arm64",
					},
				]}
				title="Build a Binary"
			/>
		</>
	)
}
