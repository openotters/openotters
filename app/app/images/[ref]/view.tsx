"use client"

import { ArtifactDetailView } from "@/components/artifact-detail-view"
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card"
import { useRouteParams } from "@/lib/use-route-params"
import { runActionForVersion } from "@/components/agents/run-version-action"

export default function ImageDetailPage() {
	const params = useRouteParams<{ ref: string }>("/images/:ref")
	const ref = params.ref ?? ""

	return (
		<ArtifactDetailView
			versionAction={runActionForVersion}
			extraSections={(describe) => {
				if (!describe) return null
				const envs = parseEnvsFromConfig(describe.config)
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
												<span className="text-muted-foreground text-xs">{env.description}</span>
											)}
										</div>
										<span className="break-all text-right font-mono text-xs">{env.value}</span>
									</div>
								))}
							</div>
						</CardContent>
					</Card>
				)
			}}
			kind="agent"
			ref={ref}
		/>
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
