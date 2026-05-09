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
	Tag,
	Trash2,
} from "lucide-react"
import Link from "next/link"
import { notFound, useRouter } from "next/navigation"
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
import { refsForDigest } from "@/lib/image-tags"
import {
	describeImage,
	listImages,
	pullAgentImage,
	removeImage,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"
import { useRouteParams } from "@/lib/use-route-params"

const BIN_ARTIFACT_TYPE = "application/vnd.openotters.bin.v1"
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

export default function ImageDetailPage() {
	const params = useRouteParams<{ ref: string }>("/images/:ref")
	const ref = params.ref ?? ""
	const router = useRouter()
	const queryClient = useQueryClient()

	const list = useQuery(listImages, {}, { enabled: ref !== "" })
	const describe = useQuery(describeImage, { ref }, { enabled: ref !== "" })

	const pull = useMutation(pullAgentImage)
	const remove = useMutation(removeImage, {
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["openotters.daemon.v1.Runtime", "ListImages"] })
			router.push("/images")
		},
	})

	if (ref === "" || list.isLoading) {
		return <p className="text-muted-foreground">Loading image…</p>
	}

	if (list.error) {
		return (
			<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
				Failed to reach daemon: {list.error.message}
			</div>
		)
	}

	const image = list.data?.images.find((i) => i.ref === ref)
	if (list.data && !image) {
		notFound()
	}
	if (!image) {
		return null
	}

	// Bin artifacts have their own detail route; redirect-via-link
	// rather than auto-redirect so the user sees the breadcrumb.
	const isBin = image.artifactType === BIN_ARTIFACT_TYPE
	// Anything that isn't an agent or bin lives in the registry as
	// shared OCI content (docker executor base images, foreign
	// pulls). Surface it as "Unknown artifact" so users don't try to
	// run / edit it as if it were an agent.
	const isUnknown = !isBin && image.artifactType !== AGENT_ARTIFACT_TYPE

	return (
		<div className="space-y-6">
			<div className="flex items-center gap-4">
				<Button asChild size="icon" variant="ghost">
					<Link href={isBin ? "/bins" : "/images"}>
						<ArrowLeft className="h-4 w-4" />
					</Link>
				</Button>
				<div className="flex flex-1 items-center gap-3">
					<div className="flex h-12 w-12 items-center justify-center rounded-lg bg-primary/10">
						<Layers className="h-6 w-6 text-primary" />
					</div>
					<div className="min-w-0 flex-1">
						<h1 className="truncate font-semibold text-2xl tracking-tight">{image.ref}</h1>
						<p className="truncate font-mono text-muted-foreground text-sm">{image.digest}</p>
					</div>
				</div>
				<div className="flex items-center gap-2">
					<Button
						disabled={pull.isPending}
						onClick={() => pull.mutate({ ref: image.ref })}
						size="sm"
						variant="outline">
						<Download className="mr-2 h-4 w-4" />
						Re-pull
					</Button>
					<Button
						className="text-destructive hover:text-destructive"
						disabled={remove.isPending}
						onClick={() => remove.mutate({ ref: image.ref })}
						size="sm"
						variant="outline">
						<Trash2 className="mr-2 h-4 w-4" />
						Delete
					</Button>
				</div>
			</div>

			{isUnknown && (
				<div className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-4 text-sm">
					<p className="font-medium">Unknown artifact type</p>
					<p className="mt-1 text-muted-foreground">
						This image's manifest carries{" "}
						<code className="font-mono text-xs">{image.artifactType || "(no artifact type)"}</code>,
						which isn't one of openotters' tracked types ({" "}
						<code className="font-mono text-xs">{AGENT_ARTIFACT_TYPE}</code> or{" "}
						<code className="font-mono text-xs">{BIN_ARTIFACT_TYPE}</code>). It lives in
						the registry but isn't a runnable agent — likely a docker executor base image
						or a third-party OCI artifact pulled into the store.
					</p>
				</div>
			)}

			{image.description && (
				<Card>
					<CardHeader className="pb-2">
						<CardTitle className="text-base">Description</CardTitle>
					</CardHeader>
					<CardContent>
						<p className="text-sm">{image.description}</p>
					</CardContent>
				</Card>
			)}

			{(() => {
				const refs = refsForDigest(list.data?.images ?? [], image.digest)
				if (refs.length <= 1) return null
				return (
					<Card>
						<CardHeader className="pb-2">
							<CardTitle className="text-base">Tags</CardTitle>
							<CardDescription>
								{refs.length} ref(s) in the registry point at this image (same digest).
							</CardDescription>
						</CardHeader>
						<CardContent>
							<div className="flex flex-wrap gap-2">
								{refs.map((r) => (
									<Link
										className="no-underline"
										href={`/images/${encodeURIComponent(r)}`}
										key={r}>
										<Badge
											className="cursor-pointer font-mono text-xs"
											variant={r === image.ref ? "default" : "secondary"}>
											<Tag className="mr-1 h-3 w-3" />
											{r}
										</Badge>
									</Link>
								))}
							</div>
						</CardContent>
					</Card>
				)
			})()}

			<Card>
				<CardHeader>
					<CardTitle className="text-base">Metadata</CardTitle>
					<CardDescription>
						Equivalent to <code className="font-mono text-xs">otters image inspect {image.ref}</code>.
					</CardDescription>
				</CardHeader>
				<CardContent className="space-y-3 text-sm">
					<Row label="Ref" mono value={image.ref} />
					<Separator />
					<Row label="Digest" mono value={image.digest} />
					<Separator />
					<Row label="Artifact type" mono value={image.artifactType || "—"} />
					<Separator />
					<Row
						label="Size"
						value={
							<span className="inline-flex items-center gap-1.5">
								<HardDrive className="h-4 w-4" />
								{formatSize(image.size)}
							</span>
						}
					/>
					<Separator />
					<Row
						label="Built"
						value={
							<span className="inline-flex items-center gap-1.5">
								<Clock className="h-4 w-4" />
								{formatDate(image.createdAt)}
							</span>
						}
					/>
					{image.source && (
						<>
							<Separator />
							<Row
								label="Source"
								value={
									<a
										className="inline-flex items-center gap-1 underline-offset-2 hover:text-foreground hover:underline"
										href={image.source}
										rel="noreferrer"
										target="_blank">
										<ExternalLink className="h-4 w-4" />
										{image.source}
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
						OCI manifest annotations. Built from <code className="font-mono text-xs">LABEL</code>{" "}
						directives in the source Agentfile.
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

			{describe.data && describe.data.config && (() => {
				const envs = parseEnvsFromConfig(describe.data.config)
				if (envs.length === 0) return null
				return (
					<Card>
						<CardHeader>
							<CardTitle className="text-base">Environment</CardTitle>
							<CardDescription>
								OS environment variables set on the spawned agent process. Built from{" "}
								<code className="font-mono text-xs">ENV</code> directives in the source Agentfile.
							</CardDescription>
						</CardHeader>
						<CardContent>
							<div className="space-y-2 text-sm">
								{envs.map((env) => (
									<div className="flex items-start justify-between gap-6" key={env.key}>
										<div className="flex flex-col">
											<span className="break-all font-mono text-xs">{env.key}</span>
											{env.description && (
												<span className="text-muted-foreground text-xs">
													{env.description}
												</span>
											)}
										</div>
										<span className="break-all text-right font-mono text-xs">{env.value}</span>
									</div>
								))}
							</div>
						</CardContent>
					</Card>
				)
			})()}

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

interface EnvDecl {
	key: string
	value: string
	description?: string
}

// parseEnvsFromConfig extracts ENV declarations from the JSON config
// blob returned by DescribeImage. Tolerates absent / malformed input
// — returns an empty array on any parse failure.
function parseEnvsFromConfig(configJSON: string): EnvDecl[] {
	try {
		const parsed = JSON.parse(configJSON)
		const envs = parsed?.agent?.envs
		if (!Array.isArray(envs)) return []
		return envs
			.filter((e): e is EnvDecl => typeof e?.key === "string" && typeof e?.value === "string")
			.map((e) => ({
				key: e.key,
				value: e.value,
				description: typeof e.description === "string" ? e.description : undefined,
			}))
	} catch {
		return []
	}
}

interface RowProps {
	label: string
	value: React.ReactNode
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
