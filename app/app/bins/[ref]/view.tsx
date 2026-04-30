"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import {
	ArrowLeft,
	Clock,
	ExternalLink,
	HardDrive,
	Terminal,
	Trash2,
} from "lucide-react"
import Link from "next/link"
import { notFound, useRouter } from "next/navigation"
import { Button } from "@/components/ui/button"
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card"
import { Separator } from "@/components/ui/separator"
import {
	describeImage,
	listImages,
	removeImage,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"
import { useRouteParams } from "@/lib/use-route-params"

const BIN_ARTIFACT_TYPE = "application/vnd.openotters.bin.v1"

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

export default function BinDetailPage() {
	const params = useRouteParams<{ ref: string }>("/bins/:ref")
	const ref = params.ref ?? ""
	const router = useRouter()
	const queryClient = useQueryClient()

	const list = useQuery(listImages, {}, { enabled: ref !== "" })
	const describe = useQuery(describeImage, { ref }, { enabled: ref !== "" })

	const remove = useMutation(removeImage, {
		onSuccess: () => {
			queryClient.invalidateQueries({ queryKey: ["openotters.daemon.v1.Runtime", "ListImages"] })
			router.push("/bins")
		},
	})

	if (ref === "" || list.isLoading) {
		return <p className="text-muted-foreground">Loading bin…</p>
	}

	if (list.error) {
		return (
			<div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">
				Failed to reach daemon: {list.error.message}
			</div>
		)
	}

	const bin = list.data?.images.find((i) => i.ref === ref && i.artifactType === BIN_ARTIFACT_TYPE)
	if (list.data && !bin) {
		notFound()
	}
	if (!bin) {
		return null
	}

	return (
		<div className="space-y-6">
			<div className="flex items-center gap-4">
				<Button asChild size="icon" variant="ghost">
					<Link href="/bins">
						<ArrowLeft className="h-4 w-4" />
					</Link>
				</Button>
				<div className="flex flex-1 items-center gap-3">
					<div className="flex h-12 w-12 items-center justify-center rounded-lg bg-primary/10">
						<Terminal className="h-6 w-6 text-primary" />
					</div>
					<div className="min-w-0 flex-1">
						<h1 className="truncate font-semibold font-mono text-2xl tracking-tight">{bin.ref}</h1>
						<p className="truncate font-mono text-muted-foreground text-sm">{bin.digest}</p>
					</div>
				</div>
				<div className="flex items-center gap-2">
					<Button
						className="text-destructive hover:text-destructive"
						disabled={remove.isPending}
						onClick={() => remove.mutate({ ref: bin.ref })}
						size="sm"
						variant="outline">
						<Trash2 className="mr-2 h-4 w-4" />
						Delete
					</Button>
				</div>
			</div>

			{bin.description && (
				<Card>
					<CardHeader className="pb-2">
						<CardTitle className="text-base">Description</CardTitle>
					</CardHeader>
					<CardContent>
						<p className="text-sm">{bin.description}</p>
					</CardContent>
				</Card>
			)}

			<Card>
				<CardHeader>
					<CardTitle className="text-base">Metadata</CardTitle>
					<CardDescription>
						Equivalent to <code className="font-mono text-xs">otters bin inspect {bin.ref}</code>.
					</CardDescription>
				</CardHeader>
				<CardContent className="space-y-3 text-sm">
					<Row label="Ref" mono value={bin.ref} />
					<Separator />
					<Row label="Digest" mono value={bin.digest} />
					<Separator />
					<Row label="Artifact type" mono value={bin.artifactType || "—"} />
					<Separator />
					<Row
						label="Size"
						value={
							<span className="inline-flex items-center gap-1.5">
								<HardDrive className="h-4 w-4" />
								{formatSize(bin.size)}
							</span>
						}
					/>
					<Separator />
					<Row
						label="Pulled"
						value={
							<span className="inline-flex items-center gap-1.5">
								<Clock className="h-4 w-4" />
								{formatDate(bin.createdAt)}
							</span>
						}
					/>
					{bin.source && (
						<>
							<Separator />
							<Row
								label="Source"
								value={
									<a
										className="inline-flex items-center gap-1 underline-offset-2 hover:text-foreground hover:underline"
										href={bin.source}
										rel="noreferrer"
										target="_blank">
										<ExternalLink className="h-4 w-4" />
										{bin.source}
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
						Manifest annotations stamped at build / push time. Reference this bin in an Agentfile via{" "}
						<code className="font-mono text-xs">BIN &lt;alias&gt; {bin.ref}</code>.
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
