"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import {
	Clock,
	Download,
	ExternalLink,
	HardDrive,
	Layers,
	MoreHorizontal,
	Plus,
	Search,
	Trash2,
} from "lucide-react"
import { useMemo, useState } from "react"
import { CliInstructionsDialog } from "@/components/cli-instructions-dialog"
import { SortSelect, SORT_DEFAULT_ID, type SortOption } from "@/components/sort-select"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import type { ImageInfo } from "@/lib/proto/v1/daemon_pb"
import { listImages, pullAgentImage, removeImage } from "@/lib/proto/v1/daemon-Runtime_connectquery"
import { useStableSort } from "@/lib/use-stable-sort"

// Bin artifacts share the registry; the Bins page is responsible for
// them, so the Images page filters them out by media type.
const BIN_ARTIFACT_TYPE = "application/vnd.openotters.bin.v1"

const SORT_OPTIONS: SortOption[] = [
	{ id: "ref-asc", label: "Ref (A→Z)" },
	{ id: "ref-desc", label: "Ref (Z→A)" },
	{ id: "size-desc", label: "Size (largest)" },
	{ id: "size-asc", label: "Size (smallest)" },
	{ id: "newest", label: "Newest first" },
	{ id: "oldest", label: "Oldest first" },
]

function compareFor(sortId: string): ((a: ImageInfo, b: ImageInfo) => number) | null {
	switch (sortId) {
		case "ref-asc":
			return (a, b) => a.ref.localeCompare(b.ref)
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
			return null
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
	return new Date(Number(unixSec) * 1000).toLocaleString("en-US", {
		month: "short",
		day: "numeric",
		year: "numeric",
		hour: "2-digit",
		minute: "2-digit",
	})
}

export default function ImagesPage() {
	const queryClient = useQueryClient()
	const [search, setSearch] = useState("")
	const [sortId, setSortId] = useState<string>(SORT_DEFAULT_ID)
	const [buildOpen, setBuildOpen] = useState(false)

	const { data, isLoading, error } = useQuery(listImages, {})

	const remove = useMutation(removeImage, {
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["openotters.daemon.v1.Runtime", "ListImages"] })
		},
	})

	const pull = useMutation(pullAgentImage)

	const all = data?.images ?? []
	const agentImages = all.filter((i) => i.artifactType !== BIN_ARTIFACT_TYPE)

	const sorted = useStableSort<ImageInfo>(
		agentImages,
		(i) => i.digest,
		useMemo(() => ({ compare: compareFor(sortId) }), [sortId]),
	)

	const filtered = sorted.filter((image) => image.ref.toLowerCase().includes(search.toLowerCase()))

	return (
		<div className="space-y-6">
			<div className="flex items-center justify-between">
				<div>
					<h1 className="font-semibold text-2xl tracking-tight">Images</h1>
					<p className="text-muted-foreground text-sm">Built Agent images in the embedded registry</p>
				</div>
				<Button onClick={() => setBuildOpen(true)}>
					<Plus className="mr-2 h-4 w-4" />
					Build Image
				</Button>
			</div>

			<CliInstructionsDialog
				description="Image building runs on the daemon. Use the otters CLI from a directory containing an Agentfile."
				footer={
					<>
						See <code className="font-mono">otters image build --help</code> for tags, build args, and remote
						registry options.
					</>
				}
				intro={
					<>
						Images are <span className="font-medium text-foreground">OCI artifacts</span> built from an
						Agentfile.
					</>
				}
				onOpenChange={setBuildOpen}
				open={buildOpen}
				steps={[
					{ caption: "From the directory containing your Agentfile", command: "otters image build ." },
					{
						caption: "Tag a build for a registry",
						command: "otters image build . -t ghcr.io/myorg/my-agent:v1.0",
					},
					{ caption: "Push to a remote registry", command: "otters image push ghcr.io/myorg/my-agent:v1.0" },
				]}
				title="Build an Image"
			/>

			{error && (
				<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
					Failed to reach daemon: {error.message}
				</div>
			)}

			{!error && (
				<div className="flex flex-wrap items-center gap-3">
					<div className="relative max-w-sm flex-1">
						<Search className="-translate-y-1/2 absolute top-1/2 left-3 h-4 w-4 text-muted-foreground" />
						<Input
							className="pl-9"
							onChange={(e) => setSearch(e.target.value)}
							placeholder="Search images..."
							value={search}
						/>
					</div>
					<SortSelect
						className="w-[220px]"
						onValueChange={setSortId}
						options={SORT_OPTIONS}
						value={sortId}
					/>
				</div>
			)}

			{isLoading && <p className="text-muted-foreground">Loading images…</p>}

			{!isLoading && !error && (
				<div className="grid gap-4">
					{filtered.map((image) => (
						<Card className="group" key={image.digest}>
							<CardHeader className="pb-3">
								<div className="flex items-start justify-between">
									<div className="flex items-center gap-3">
										<div className="flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10">
											<Layers className="h-5 w-5 text-primary" />
										</div>
										<div>
											<CardTitle className="font-medium text-base">{image.ref}</CardTitle>
											<CardDescription className="font-mono text-xs">
												{image.digest.substring(0, 19)}…
											</CardDescription>
										</div>
									</div>
									<DropdownMenu>
										<DropdownMenuTrigger asChild>
											<Button className="h-8 w-8" size="icon" variant="ghost">
												<MoreHorizontal className="h-4 w-4" />
											</Button>
										</DropdownMenuTrigger>
										<DropdownMenuContent align="end">
											<DropdownMenuItem
												disabled={pull.isPending}
												onClick={() => pull.mutate({ ref: image.ref })}>
												<Download className="mr-2 h-4 w-4" />
												Re-pull
											</DropdownMenuItem>
											<DropdownMenuSeparator />
											<DropdownMenuItem
												className="text-destructive"
												disabled={remove.isPending}
												onClick={() => remove.mutate({ ref: image.ref })}>
												<Trash2 className="mr-2 h-4 w-4" />
												Delete image
											</DropdownMenuItem>
										</DropdownMenuContent>
									</DropdownMenu>
								</div>
							</CardHeader>
							<CardContent className="space-y-3">
								{image.description && (
									<p className="text-muted-foreground text-sm">{image.description}</p>
								)}
								<div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-muted-foreground text-sm">
									<div className="flex items-center gap-1.5">
										<HardDrive className="h-4 w-4" />
										<span>{formatSize(image.size)}</span>
									</div>
									<div className="flex items-center gap-1.5">
										<Clock className="h-4 w-4" />
										<span>Built {formatDate(image.createdAt)}</span>
									</div>
									{image.source && (
										<a
											className="inline-flex items-center gap-1 underline-offset-2 hover:text-foreground hover:underline"
											href={image.source}
											rel="noreferrer"
											target="_blank">
											<ExternalLink className="h-4 w-4" />
											<span className="max-w-[40ch] truncate">{image.source}</span>
										</a>
									)}
								</div>
							</CardContent>
						</Card>
					))}
				</div>
			)}

			{!isLoading && !error && filtered.length === 0 && (
				<Card className="py-12">
					<CardContent className="flex flex-col items-center justify-center text-center">
						<Layers className="mb-4 h-12 w-12 text-muted-foreground/50" />
						<h3 className="font-medium">No images found</h3>
						<p className="mt-1 text-muted-foreground text-sm">
							{search ? "Try adjusting your search" : "Build your first Agentfile image to get started"}
						</p>
					</CardContent>
				</Card>
			)}
		</div>
	)
}
