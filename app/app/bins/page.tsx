"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { Clock, HardDrive, MoreVertical, Terminal, Trash2 } from "lucide-react"
import { useMemo, useState } from "react"
import { AddBinButton } from "@/components/add-bin-button"
import { SortSelect, type SortOption } from "@/components/sort-select"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import type { ImageInfo } from "@/lib/proto/v1/daemon_pb"
import { listImages, removeImage } from "@/lib/proto/v1/daemon-Runtime_connectquery"
import { useStableSort } from "@/lib/use-stable-sort"

const BIN_ARTIFACT_TYPE = "application/vnd.openotters.bin.v1"

const SORT_OPTIONS: SortOption[] = [
	{ id: "ref-asc", label: "Ref (A→Z)" },
	{ id: "ref-desc", label: "Ref (Z→A)" },
	{ id: "size-desc", label: "Size (largest)" },
	{ id: "size-asc", label: "Size (smallest)" },
	{ id: "newest", label: "Newest first" },
	{ id: "oldest", label: "Oldest first" },
]

const REF_ASC = (a: ImageInfo, b: ImageInfo) => a.ref.localeCompare(b.ref)

function compareFor(sortId: string): (a: ImageInfo, b: ImageInfo) => number {
	switch (sortId) {
		case "ref-desc":
			return (a, b) => b.ref.localeCompare(a.ref)
		case "size-desc":
			return (a, b) => Number(b.size - a.size)
		case "size-asc":
			return (a, b) => Number(a.size - b.size)
		case "newest":
			return (a, b) => Number(b.createdAt - a.createdAt)
		case "oldest":
			return (a, b) => Number(a.createdAt - b.createdAt)
		default:
			return REF_ASC
	}
}

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

	const [sortId, setSortId] = useState<string>("ref-asc")

	const bins = (data?.images ?? []).filter((i) => i.artifactType === BIN_ARTIFACT_TYPE)

	const compare = useMemo(() => compareFor(sortId), [sortId])
	const sorted = useStableSort<ImageInfo>(bins, (b) => b.digest, compare)

	return (
		<div className="space-y-6">
			<div className="flex items-center justify-between">
				<div>
					<h1 className="font-semibold text-2xl tracking-tight">Bins</h1>
					<p className="text-muted-foreground">
						Binary tool images. Reference these in an Agentfile via the{" "}
						<code className="font-mono text-xs">BIN</code> directive.
					</p>
				</div>
				<AddBinButton />
			</div>

			{error && (
				<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
					Failed to reach daemon: {error.message}
				</div>
			)}

			{!error && bins.length > 0 && (
				<div className="flex flex-wrap items-center gap-3">
					<SortSelect
						className="w-[220px]"
						onValueChange={setSortId}
						options={SORT_OPTIONS}
						value={sortId}
					/>
				</div>
			)}

			{isLoading && <p className="text-muted-foreground">Loading bins…</p>}

			{!isLoading && !error && sorted.length > 0 && (
				<div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
					{sorted.map((bin) => (
						<Card className="group" key={bin.digest}>
							<CardHeader className="flex flex-row items-start justify-between space-y-0 pb-2">
								<div className="flex items-start gap-3">
									<div className="flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10">
										<Terminal className="h-5 w-5 text-primary" />
									</div>
									<div>
										<CardTitle className="font-mono text-base">{bin.ref}</CardTitle>
										<CardDescription className="font-mono text-xs">
											{bin.digest.substring(0, 19)}…
										</CardDescription>
									</div>
								</div>
								<DropdownMenu>
									<DropdownMenuTrigger asChild>
										<Button className="h-8 w-8" size="icon" variant="ghost">
											<MoreVertical className="h-4 w-4" />
										</Button>
									</DropdownMenuTrigger>
									<DropdownMenuContent align="end">
										<DropdownMenuItem
											className="text-destructive"
											disabled={remove.isPending}
											onClick={() => remove.mutate({ ref: bin.ref })}>
											<Trash2 className="mr-2 h-4 w-4" />
											Remove
										</DropdownMenuItem>
									</DropdownMenuContent>
								</DropdownMenu>
							</CardHeader>
							<CardContent>
								<div className="flex items-center gap-4 text-muted-foreground text-xs">
									<div className="flex items-center gap-1.5">
										<HardDrive className="h-3 w-3" />
										<span>{formatSize(bin.size)}</span>
									</div>
									<div className="flex items-center gap-1.5">
										<Clock className="h-3 w-3" />
										<span>{formatDate(bin.createdAt)}</span>
									</div>
								</div>
							</CardContent>
						</Card>
					))}
				</div>
			)}

			{!isLoading && !error && bins.length === 0 && (
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
