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
	Tag,
	Trash2,
} from "lucide-react"
import Link from "next/link"
import { useMemo, useState } from "react"
import { toast } from "sonner"
import { CliInstructionsDialog } from "@/components/cli-instructions-dialog"
import { ConfirmDelete } from "@/components/confirm-delete"
import { PageHeader } from "@/components/page-header"
import { PullFromUrlButton } from "@/components/pull-from-url-button"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { groupImagesByName } from "@/lib/image-tags"
import {
	listImages,
	pullAgentImage,
	removeImage,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"

// The registry holds agent images, bin tool images, and any other
// OCI artifacts the daemon has pulled (docker executor base images,
// arbitrary pulls, etc.). The Images page is the agent-image surface,
// so we positively filter on the agent artifact type — unknown
// artifacts go to the appropriate other view, or stay invisible if
// they're third-party OCI content the daemon happens to be hosting.
const AGENT_ARTIFACT_TYPE = "application/vnd.openotters.agent.v1"

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
	const [buildOpen, setBuildOpen] = useState(false)

	const { data, isLoading, error } = useQuery(listImages, {})

	const remove = useMutation(removeImage, {
		onSuccess: (_data, vars) => {
			queryClient.invalidateQueries({ queryKey: ["openotters.daemon.v1.Runtime", "ListImages"] })
			toast.success(`Removed ${vars.ref}`)
		},
		onError: (err, vars) => {
			toast.error(`Remove failed: ${vars.ref}`, { description: err.message })
		},
	})

	const pull = useMutation(pullAgentImage, {
		onMutate: (vars) => ({ toastId: toast.loading(`Pulling ${vars.ref}…`) }),
		onSuccess: (_data, vars, ctx) => {
			queryClient.invalidateQueries({ queryKey: ["openotters.daemon.v1.Runtime", "ListImages"] })
			toast.success(`Pulled ${vars.ref}`, { id: ctx?.toastId })
		},
		onError: (err, vars, ctx) => {
			toast.error(`Pull failed: ${vars.ref}`, { description: err.message, id: ctx?.toastId })
		},
	})

	const groups = useMemo(() => {
		const all = data?.images ?? []
		const filtered = all.filter((i) => i.artifactType === AGENT_ARTIFACT_TYPE)
		return groupImagesByName(filtered)
	}, [data])

	return (
		<div className="space-y-6">
			<PageHeader
				actions={
					<div className="flex items-center gap-2">
						<PullFromUrlButton placeholder="ghcr.io/myorg/my-agent:latest" />
						<Button onClick={() => setBuildOpen(true)}>
							<Plus className="mr-2 h-4 w-4" />
							Build Image
						</Button>
					</div>
				}
				command="otters image ls"
				description="Built agent images in the embedded registry."
				title="Images"
			/>

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

			{isLoading && <p className="text-muted-foreground">Loading images…</p>}

			{!isLoading && !error && groups.length > 0 && (
				<div className="grid gap-4">
					{groups.map((group) => {
						const image = group.primary
						const versions = group.digests.length
						return (
							<Card className="group transition-colors hover:bg-muted/50" key={group.name}>
								<CardHeader className="pb-3">
									<div className="flex items-start justify-between">
										<Link
											aria-label={`Open ${group.name} details`}
											className="flex flex-1 items-center gap-3"
											href={`/images/${encodeURIComponent(image.ref)}`}>
											<div className="flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10">
												<Layers className="h-5 w-5 text-primary" />
											</div>
											<div className="min-w-0 flex-1">
												<div className="flex items-center gap-2">
													<CardTitle className="font-medium text-base">
														{group.name}
													</CardTitle>
													<Badge className="font-mono text-xs" variant="secondary">
														<Tag className="mr-1 h-3 w-3" />
														{versions} version{versions === 1 ? "" : "s"}
													</Badge>
												</div>
												<CardDescription className="font-mono text-xs">
													{image.digest.substring(0, 19)}…
												</CardDescription>
											</div>
										</Link>
										<DropdownMenu>
											<DropdownMenuTrigger asChild>
												<Button className="h-8 w-8" size="icon" variant="ghost">
													<MoreHorizontal className="h-4 w-4" />
												</Button>
											</DropdownMenuTrigger>
											<DropdownMenuContent align="end">
												<DropdownMenuItem asChild>
													<Link href={`/images/${encodeURIComponent(image.ref)}`}>
														<ExternalLink className="mr-2 h-4 w-4" />
														Details
													</Link>
												</DropdownMenuItem>
												<DropdownMenuItem
													disabled={pull.isPending}
													onClick={() => pull.mutate({ ref: image.ref })}>
													<Download className="mr-2 h-4 w-4" />
													Re-pull
												</DropdownMenuItem>
												<DropdownMenuSeparator />
												<ConfirmDelete
													description={
														<>
															This removes every tag under{" "}
															<code className="font-mono text-xs">{group.name}</code> from the
															local registry ({versions} version
															{versions === 1 ? "" : "s"}).
														</>
													}
													onConfirm={() => {
														for (const dg of group.digests) {
															for (const ref of dg.refs) {
																remove.mutate({ ref })
															}
														}
													}}
													pending={remove.isPending}
													title="Delete image?"
													trigger={(open) => (
														<DropdownMenuItem
															className="text-destructive"
															disabled={remove.isPending}
															onSelect={(e) => {
																e.preventDefault()
																open()
															}}>
															<Trash2 className="mr-2 h-4 w-4" />
															Delete image
														</DropdownMenuItem>
													)}
												/>
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
						)
					})}
				</div>
			)}

			{!isLoading && !error && groups.length === 0 && (
				<Card className="py-12">
					<CardContent className="flex flex-col items-center justify-center text-center">
						<Layers className="mb-4 h-12 w-12 text-muted-foreground/50" />
						<h3 className="font-medium">No images found</h3>
						<p className="mt-1 text-muted-foreground text-sm">
							Build your first Agentfile image to get started
						</p>
					</CardContent>
				</Card>
			)}
		</div>
	)
}
