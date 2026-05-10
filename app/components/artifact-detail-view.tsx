"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import {
	ArrowLeft,
	Clock,
	Download,
	ExternalLink,
	HardDrive,
	Layers,
	Terminal,
	Trash2,
	Upload,
} from "lucide-react"
import Link from "next/link"
import { notFound, useRouter } from "next/navigation"
import type { ReactNode } from "react"
import { toast } from "sonner"
import { ConfirmDelete } from "@/components/confirm-delete"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card"
import { Separator } from "@/components/ui/separator"
import { parseRefTag, versionsForRef } from "@/lib/image-tags"
import {
	describeImage,
	listImages,
	pullAgentImage,
	pushAgentImage,
	removeImage,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"

const AGENT_ARTIFACT_TYPE = "application/vnd.openotters.agent.v1"
const BIN_ARTIFACT_TYPE = "application/vnd.openotters.bin.v1"

export type ArtifactKind = "agent" | "bin"

interface ArtifactDetailViewProps {
	ref: string
	kind: ArtifactKind
	// extraSections render below the Labels card. Used by the agent
	// view to surface ENV declarations parsed from the manifest config.
	extraSections?: (describeData: { config: string } | undefined) => ReactNode
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

function kindConfig(kind: ArtifactKind) {
	if (kind === "agent") {
		return {
			artifactType: AGENT_ARTIFACT_TYPE,
			listingHref: "/images",
			detailHref: (ref: string) => `/images/${encodeURIComponent(ref)}`,
			Icon: Layers,
			loadingLabel: "Loading image…",
			cliNoun: "image",
			cliInspectExample: "otters image inspect",
			pulledLabel: "Built",
		}
	}
	return {
		artifactType: BIN_ARTIFACT_TYPE,
		listingHref: "/bins",
		detailHref: (ref: string) => `/bins/${encodeURIComponent(ref)}`,
		Icon: Terminal,
		loadingLabel: "Loading bin…",
		cliNoun: "bin",
		cliInspectExample: "otters bin inspect",
		pulledLabel: "Pulled",
	}
}

export function ArtifactDetailView({ ref, kind, extraSections }: ArtifactDetailViewProps) {
	const cfg = kindConfig(kind)
	const router = useRouter()
	const queryClient = useQueryClient()

	const list = useQuery(listImages, {}, { enabled: ref !== "" })
	const describe = useQuery(describeImage, { ref }, { enabled: ref !== "" })

	const invalidateLists = () => {
		queryClient.invalidateQueries({ queryKey: ["openotters.daemon.v1.Runtime", "ListImages"] })
		queryClient.invalidateQueries({ queryKey: ["openotters.daemon.v1.Runtime", "DescribeImage"] })
	}

	const pull = useMutation(pullAgentImage, {
		onMutate: (vars) => ({
			toastId: toast.loading(`Pulling ${vars.ref}…`),
		}),
		onSuccess: (_data, vars, ctx) => {
			invalidateLists()
			toast.success(`Pulled ${vars.ref}`, { id: ctx?.toastId })
		},
		onError: (err, vars, ctx) => {
			toast.error(`Pull failed: ${vars.ref}`, {
				description: err.message,
				id: ctx?.toastId,
			})
		},
	})
	const push = useMutation(pushAgentImage, {
		onMutate: (vars) => ({
			toastId: toast.loading(`Pushing ${vars.ref}…`),
		}),
		onSuccess: (_data, vars, ctx) => {
			toast.success(`Pushed ${vars.ref}`, { id: ctx?.toastId })
		},
		onError: (err, vars, ctx) => {
			toast.error(`Push failed: ${vars.ref}`, {
				description: err.message,
				id: ctx?.toastId,
			})
		},
	})
	const remove = useMutation(removeImage, {
		onSuccess: (_data, vars) => {
			invalidateLists()
			toast.success(`Removed ${vars.ref}`)
			if (vars.ref === ref) {
				router.push(cfg.listingHref)
			}
		},
		onError: (err, vars) => {
			toast.error(`Remove failed: ${vars.ref}`, { description: err.message })
		},
	})

	if (ref === "" || list.isLoading) {
		return <p className="text-muted-foreground">{cfg.loadingLabel}</p>
	}

	if (list.error) {
		return (
			<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
				Failed to reach daemon: {list.error.message}
			</div>
		)
	}

	const all = list.data?.images ?? []
	const artifact = all.find((i) => i.ref === ref && i.artifactType === cfg.artifactType)
	if (list.data && !artifact) {
		notFound()
	}
	if (!artifact) {
		return null
	}

	const versions = versionsForRef(all.filter((i) => i.artifactType === cfg.artifactType), ref)
	const Icon = cfg.Icon

	return (
		<div className="space-y-6">
			<div className="flex items-center gap-4">
				<Button asChild size="icon" variant="ghost">
					<Link href={cfg.listingHref}>
						<ArrowLeft className="h-4 w-4" />
					</Link>
				</Button>
				<div className="flex flex-1 items-center gap-3">
					<div className="flex h-12 w-12 items-center justify-center rounded-lg bg-primary/10">
						<Icon className="h-6 w-6 text-primary" />
					</div>
					<div className="min-w-0 flex-1">
						<h1 className="truncate font-semibold font-mono text-2xl tracking-tight">
							{artifact.ref}
						</h1>
						<p className="truncate font-mono text-muted-foreground text-sm">{artifact.digest}</p>
					</div>
				</div>
			</div>

			{artifact.description && (
				<Card>
					<CardHeader className="pb-2">
						<CardTitle className="text-base">Description</CardTitle>
					</CardHeader>
					<CardContent>
						<p className="text-sm">{artifact.description}</p>
					</CardContent>
				</Card>
			)}

			<Card>
				<CardHeader>
					<CardTitle className="text-base">Versions</CardTitle>
					<CardDescription>
						{versions.length} version{versions.length === 1 ? "" : "s"} of this {cfg.cliNoun}{" "}
						in the registry. Pull, push, or delete each tag independently.
					</CardDescription>
				</CardHeader>
				<CardContent className="space-y-2">
					{versions.map((v) => {
						const tag = parseRefTag(v.ref) || "(no tag)"
						const isCurrent = v.ref === artifact.ref
						const isAlias = v.digest === artifact.digest && !isCurrent
						const pullPending = pull.isPending && pull.variables?.ref === v.ref
						const pushPending = push.isPending && push.variables?.ref === v.ref
						const removePending = remove.isPending && remove.variables?.ref === v.ref
						return (
							<div
								className={`flex flex-wrap items-center gap-3 rounded-lg border p-3 ${isCurrent ? "border-primary/40 bg-primary/5" : "border-border"}`}
								key={v.ref}>
								<div className="min-w-0 flex-1">
									<div className="flex items-center gap-2">
										<Link
											className="font-mono font-medium text-sm hover:underline"
											href={cfg.detailHref(v.ref)}>
											{tag}
										</Link>
										{isCurrent && (
											<Badge className="text-xs" variant="default">
												current
											</Badge>
										)}
										{isAlias && (
											<Badge className="text-xs" variant="secondary">
												same digest
											</Badge>
										)}
									</div>
									<div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-muted-foreground text-xs">
										<span className="break-all font-mono">{v.digest.substring(0, 19)}…</span>
										<span className="inline-flex items-center gap-1">
											<HardDrive className="h-3 w-3" />
											{formatSize(v.size)}
										</span>
										<span className="inline-flex items-center gap-1">
											<Clock className="h-3 w-3" />
											{cfg.pulledLabel} {formatDate(v.createdAt)}
										</span>
									</div>
								</div>
								<div className="flex items-center gap-1">
									<Button
										disabled={pullPending}
										onClick={() => pull.mutate({ ref: v.ref })}
										size="sm"
										title={`Pull ${v.ref} from remote registry`}
										variant="outline">
										<Download className={`h-4 w-4 ${pullPending ? "animate-pulse" : ""}`} />
										<span className="sr-only">Pull</span>
									</Button>
									<Button
										disabled={pushPending}
										onClick={() => push.mutate({ ref: v.ref })}
										size="sm"
										title={`Push ${v.ref} to remote registry`}
										variant="outline">
										<Upload className={`h-4 w-4 ${pushPending ? "animate-pulse" : ""}`} />
										<span className="sr-only">Push</span>
									</Button>
									<ConfirmDelete
										description={
											<>
												This removes <code className="font-mono text-xs">{v.ref}</code> from
												the local registry.{" "}
												{v.digest === artifact.digest && versions.some((other) => other.ref !== v.ref && other.digest === v.digest) ? (
													<>Other tags pointing at the same digest will be untagged too.</>
												) : null}
											</>
										}
										onConfirm={() => remove.mutate({ ref: v.ref })}
										pending={removePending}
										title={`Delete ${parseRefTag(v.ref) || v.ref}?`}
										trigger={(open) => (
											<Button
												className="text-destructive hover:text-destructive"
												disabled={removePending}
												onClick={open}
												size="sm"
												title={`Delete ${v.ref} from local registry`}
												variant="outline">
												<Trash2 className="h-4 w-4" />
												<span className="sr-only">Delete</span>
											</Button>
										)}
									/>
								</div>
							</div>
						)
					})}
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle className="text-base">Metadata</CardTitle>
					<CardDescription>
						Equivalent to{" "}
						<code className="font-mono text-xs">
							{cfg.cliInspectExample} {artifact.ref}
						</code>
						.
					</CardDescription>
				</CardHeader>
				<CardContent className="space-y-3 text-sm">
					<Row label="Ref" mono value={artifact.ref} />
					<Separator />
					<Row label="Digest" mono value={artifact.digest} />
					<Separator />
					<Row label="Artifact type" mono value={artifact.artifactType || "—"} />
					<Separator />
					<Row
						label="Size"
						value={
							<span className="inline-flex items-center gap-1.5">
								<HardDrive className="h-4 w-4" />
								{formatSize(artifact.size)}
							</span>
						}
					/>
					<Separator />
					<Row
						label={cfg.pulledLabel}
						value={
							<span className="inline-flex items-center gap-1.5">
								<Clock className="h-4 w-4" />
								{formatDate(artifact.createdAt)}
							</span>
						}
					/>
					{artifact.source && (
						<>
							<Separator />
							<Row
								label="Source"
								value={
									<a
										className="inline-flex items-center gap-1 underline-offset-2 hover:text-foreground hover:underline"
										href={artifact.source}
										rel="noreferrer"
										target="_blank">
										<ExternalLink className="h-4 w-4" />
										{artifact.source}
									</a>
								}
							/>
						</>
					)}
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle className="text-base">Labels</CardTitle>
					<CardDescription>
						OCI manifest annotations stamped at build / push time.
					</CardDescription>
				</CardHeader>
				<CardContent>
					{describe.isLoading && (
						<p className="text-muted-foreground text-sm">Loading manifest…</p>
					)}
					{describe.error && (
						<p className="text-destructive text-sm">
							Failed to inspect manifest: {describe.error.message}
						</p>
					)}
					{!describe.isLoading &&
						!describe.error &&
						describe.data &&
						Object.keys(describe.data.labels).length === 0 && (
							<p className="text-muted-foreground text-sm">No labels stamped on the manifest.</p>
						)}
					{describe.data && Object.keys(describe.data.labels).length > 0 && (
						<div className="space-y-2 text-sm">
							{Object.entries(describe.data.labels)
								.sort(([a], [b]) => a.localeCompare(b))
								.map(([key, value]) => (
									<div className="flex items-start justify-between gap-6" key={key}>
										<span className="break-all font-mono text-muted-foreground text-xs">
											{key}
										</span>
										<span className="break-all text-right font-mono text-xs">{value}</span>
									</div>
								))}
						</div>
					)}
				</CardContent>
			</Card>

			{extraSections?.(describe.data ? { config: describe.data.config } : undefined)}

			{describe.data && describe.data.layers.length > 0 && (
				<Card>
					<CardHeader>
						<CardTitle className="text-base">Layers</CardTitle>
						<CardDescription>{describe.data.layers.length} layer(s) in the manifest.</CardDescription>
					</CardHeader>
					<CardContent>
						<ol className="space-y-1 font-mono text-muted-foreground text-xs">
							{describe.data.layers.map((digest, idx) => (
								<li className="break-all" key={`${idx}-${digest}`}>
									{idx + 1}. {digest}
								</li>
							))}
						</ol>
					</CardContent>
				</Card>
			)}
		</div>
	)
}

interface RowProps {
	label: string
	value: ReactNode
	mono?: boolean
}

function Row({ label, value, mono }: RowProps) {
	return (
		<div className="flex items-start justify-between gap-6">
			<span className="shrink-0 text-muted-foreground">{label}</span>
			<span className={`break-all text-right ${mono ? "font-mono text-xs" : ""}`}>{value}</span>
		</div>
	)
}
