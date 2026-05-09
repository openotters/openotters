"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import {
	Clock,
	ExternalLink,
	HardDrive,
	MoreVertical,
	Tag,
	Terminal,
	Trash2,
} from "lucide-react"
import Link from "next/link"
import { useMemo } from "react"
import { AddBinButton } from "@/components/add-bin-button"
import { PageHeader } from "@/components/page-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { groupImagesByDigest } from "@/lib/image-tags"
import { listImages, removeImage } from "@/lib/proto/v1/daemon-Runtime_connectquery"

const BIN_ARTIFACT_TYPE = "application/vnd.openotters.bin.v1"

function formatSize(bytes: bigint): string {
	const n = Number(bytes)
	if (n >= 1_073_741_824) return `${(n / 1_073_741_824).toFixed(1)} GB`
	if (n >= 1_048_576) return `${(n / 1_048_576).toFixed(1)} MB`
	if (n >= 1_024) return `${(n / 1_024).toFixed(1)} KB`
	return `${n} B`
}

function formatDate(unixSec: bigint): string {
	return new Date(Number(unixSec) * 1000).toLocaleDateString("en-US", {
		month: "short",
		day: "numeric",
		year: "numeric",
	})
}

// Bins surface from the same registry as Agent Images, distinguished
// by OCI artifact type. No dedicated ListBins RPC exists yet; if one
// lands later, swap this page over with no UI change.
export default function BinsPage() {
	const queryClient = useQueryClient()
	const { data, isLoading, error } = useQuery(listImages, {})
	const remove = useMutation(removeImage, {
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["openotters.daemon.v1.Runtime", "ListImages"] })
		},
	})

	const groups = useMemo(() => {
		const all = data?.images ?? []
		const filtered = all.filter((i) => i.artifactType === BIN_ARTIFACT_TYPE)
		return groupImagesByDigest(filtered)
	}, [data])

	return (
		<div className="space-y-6">
			<PageHeader
				actions={<AddBinButton />}
				command="otters bin ls"
				description={
					<>
						Binary tool images. Reference these in an Agentfile via the{" "}
						<code className="font-mono text-xs">BIN</code> directive.
					</>
				}
				title="Bins"
			/>

			{error && (
				<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
					Failed to reach daemon: {error.message}
				</div>
			)}

			{isLoading && <p className="text-muted-foreground">Loading bins…</p>}

			{!isLoading && !error && groups.length > 0 && (
				<div className="grid gap-4">
					{groups.map((group) => {
						const bin = group.primary
						const extraTags = group.refs.length - 1
						return (
						<Card className="group transition-colors hover:bg-muted/50" key={bin.digest}>
							<CardHeader className="pb-3">
								<div className="flex items-start justify-between">
									<Link
										aria-label={`Open ${bin.ref} details`}
										className="flex flex-1 items-center gap-3"
										href={`/bins/${encodeURIComponent(bin.ref)}`}>
										<div className="flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10">
											<Terminal className="h-5 w-5 text-primary" />
										</div>
										<div className="min-w-0 flex-1">
											<div className="flex items-center gap-2">
												<CardTitle className="font-mono text-base">{bin.ref}</CardTitle>
												{extraTags > 0 && (
													<Badge className="font-mono text-xs" variant="secondary">
														<Tag className="mr-1 h-3 w-3" />
														+{extraTags}
													</Badge>
												)}
											</div>
											<CardDescription className="font-mono text-xs">
												{bin.digest.substring(0, 19)}…
											</CardDescription>
										</div>
									</Link>
									<DropdownMenu>
										<DropdownMenuTrigger asChild>
											<Button className="h-8 w-8" size="icon" variant="ghost">
												<MoreVertical className="h-4 w-4" />
											</Button>
										</DropdownMenuTrigger>
										<DropdownMenuContent align="end">
											<DropdownMenuItem asChild>
												<Link href={`/bins/${encodeURIComponent(bin.ref)}`}>
													<ExternalLink className="mr-2 h-4 w-4" />
													Details
												</Link>
											</DropdownMenuItem>
											<DropdownMenuItem
												className="text-destructive"
												disabled={remove.isPending}
												onClick={() => remove.mutate({ ref: bin.ref })}>
												<Trash2 className="mr-2 h-4 w-4" />
												Remove
											</DropdownMenuItem>
										</DropdownMenuContent>
									</DropdownMenu>
								</div>
							</CardHeader>
							<CardContent>
								<div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-muted-foreground text-sm">
									<div className="flex items-center gap-1.5">
										<HardDrive className="h-4 w-4" />
										<span>{formatSize(bin.size)}</span>
									</div>
									<div className="flex items-center gap-1.5">
										<Clock className="h-4 w-4" />
										<span>Built {formatDate(bin.createdAt)}</span>
									</div>
								</div>
							</CardContent>
						</Card>
						)
					})}
				</div>
			)}

			{!isLoading && !error && groups.length === 0 && (
				<Card className="border-dashed py-12">
					<CardContent className="flex flex-col items-center justify-center text-center">
						<Terminal className="mb-4 h-12 w-12 text-muted-foreground/50" />
						<h3 className="font-medium">No bins yet</h3>
						<p className="mt-1 text-muted-foreground text-sm">
							Pull or build a binary image to expose tools to your agents.
						</p>
					</CardContent>
				</Card>
			)}
		</div>
	)
}
