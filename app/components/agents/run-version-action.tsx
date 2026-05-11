"use client"

import { useQuery } from "@connectrpc/connect-query"
import { Play } from "lucide-react"
import { useMemo } from "react"
import {
	type ImageEnv,
	RunFromImageDialog,
} from "@/components/agents/run-from-image-dialog"
import { Button } from "@/components/ui/button"
import { describeImage } from "@/lib/proto/v1/daemon-Runtime_connectquery"

// RunActionForVersion renders a "Run" button alongside the per-row
// version actions (Pull / Push / Delete) inside the Versions card on
// the image detail page. Each version row gets its own dialog
// instance — clicking Run on a specific tag launches an agent from
// that exact tag rather than from whatever's "current".
//
// The component fetches describe data for its specific ref so the
// dialog opens with the right env declarations pre-populated. Cache
// is shared with the rest of the page via React Query keying on
// the ref string.
function RunActionForVersion({ ref }: { ref: string }) {
	const desc = useQuery(describeImage, { ref })

	const envs: ImageEnv[] = useMemo(() => {
		if (!desc.data?.config) return []
		try {
			const parsed = JSON.parse(desc.data.config) as {
				agent?: { envs?: ImageEnv[] }
			}
			return parsed?.agent?.envs ?? []
		} catch {
			return []
		}
	}, [desc.data])

	return (
		<RunFromImageDialog
			envs={envs}
			imageRef={ref}
			trigger={
				<Button
					disabled={desc.isLoading}
					size="sm"
					title={`Run an agent from ${ref}`}
					variant="outline">
					<Play className="h-4 w-4" />
					<span className="sr-only">Run</span>
				</Button>
			}
		/>
	)
}

// runActionForVersion is the function shape ArtifactDetailView's
// versionAction prop expects. Exported as a function (not a JSX
// element) so the parent can call it inline with the row's ref.
// Bin-kind images don't render this slot.
export function runActionForVersion(ref: string): React.ReactNode {
	return <RunActionForVersion ref={ref} />
}
