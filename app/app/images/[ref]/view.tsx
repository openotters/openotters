"use client"

import { useQuery } from "@connectrpc/connect-query"
import { KeyRound } from "lucide-react"
import { ArtifactDetailView } from "@/components/artifact-detail-view"
import {
	Accordion,
	AccordionContent,
	AccordionItem,
	AccordionTrigger,
} from "@/components/ui/accordion"
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card"
import { listCapabilities } from "@/lib/proto/v1/daemon-Runtime_connectquery"
import { useRouteParams } from "@/lib/use-route-params"
import { runActionForVersion } from "@/components/agents/run-version-action"

export default function ImageDetailPage() {
	const params = useRouteParams<{ ref: string }>("/images/:ref")
	const ref = params.ref ?? ""

	// Catalogue is image-independent (daemon-version-scoped); fetch
	// it once at page mount so the Capabilities section can render
	// names + descriptions without a per-name lookup.
	const catalogue = useQuery(listCapabilities, {})
	const capDescriptions = new Map<string, string>()
	for (const c of catalogue.data?.capabilities ?? []) {
		capDescriptions.set(c.name, c.description)
	}

	return (
		<ArtifactDetailView
			versionAction={runActionForVersion}
			extraSections={(describe) => {
				if (!describe) return null
				const envs = parseEnvsFromConfig(describe.config)
				const contexts = parseContextsFromConfig(describe.config)
				const capabilities = parseCapabilitiesFromConfig(describe.config)
				return (
					<>
						{capabilities.length > 0 && (
							<Card>
								<CardHeader>
									<CardTitle className="text-base">
										Capabilities ({capabilities.length})
									</CardTitle>
									<CardDescription>
										Runtime tool surface declared by this image's{" "}
										<code className="font-mono text-xs">CAPABILITY</code>{" "}
										directives. Operators may append more at run-time with{" "}
										<code className="font-mono text-xs">--cap</code>; the
										final set lives on the agent's JWT.
									</CardDescription>
								</CardHeader>
								<CardContent>
									<div className="space-y-2">
										{capabilities.map((name) => (
											<div className="rounded-lg border p-3" key={name}>
												<div className="flex items-center gap-2">
													<KeyRound className="h-4 w-4 text-primary" />
													<code className="font-mono font-medium text-sm">
														{name}
													</code>
												</div>
												{capDescriptions.has(name) && (
													<p className="mt-1 pl-6 text-muted-foreground text-xs">
														{capDescriptions.get(name)}
													</p>
												)}
											</div>
										))}
									</div>
								</CardContent>
							</Card>
						)}

						{contexts.length > 0 && (
							<Card>
								<CardHeader>
									<CardTitle className="text-base">Contexts</CardTitle>
									<CardDescription>
										Markdown files baked into the agent at{" "}
										<code className="font-mono text-xs">/etc/context/</code> and loaded
										into the model's system prompt at run time. Click a name to view its
										content as the model sees it.
									</CardDescription>
								</CardHeader>
								<CardContent>
									<Accordion className="w-full" collapsible type="single">
										{contexts.map((ctx) => (
											<AccordionItem key={ctx.name} value={ctx.name}>
												<AccordionTrigger>
													<div className="flex min-w-0 flex-1 flex-col items-start gap-0.5 pr-3 text-left">
														<span className="break-all font-medium font-mono text-sm">
															{ctx.name}
														</span>
														{ctx.description && (
															<span className="text-muted-foreground text-xs">
																{ctx.description}
															</span>
														)}
													</div>
												</AccordionTrigger>
												<AccordionContent>
													{ctx.content ? (
														<pre className="overflow-x-auto whitespace-pre-wrap break-words rounded-md bg-muted p-3 font-mono text-xs">
															{ctx.content}
														</pre>
													) : (
														<p className="text-muted-foreground text-sm italic">
															No content recorded for this context.
														</p>
													)}
												</AccordionContent>
											</AccordionItem>
										))}
									</Accordion>
								</CardContent>
							</Card>
						)}

						{envs.length > 0 && (
							<Card>
								<CardHeader>
									<CardTitle className="text-base">Environment</CardTitle>
									<CardDescription>
										OS environment variables set on the spawned agent process. Built from{" "}
										<code className="font-mono text-xs">ENV</code> directives in the
										source Agentfile.
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
												<span className="break-all text-right font-mono text-xs">
													{env.value}
												</span>
											</div>
										))}
									</div>
								</CardContent>
							</Card>
						)}
					</>
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

interface ContextDecl {
	name: string
	description?: string
	content?: string
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

// parseCapabilitiesFromConfig extracts the agent's CAPABILITY
// declarations. The spec serialises them as a flat string array
// (`agent.capabilities`); descriptions are looked up server-side
// via ListCapabilities since the image config only stores names.
// Tolerates absent / malformed input — empty array on any parse
// failure.
function parseCapabilitiesFromConfig(configJSON: string): string[] {
	try {
		const parsed = JSON.parse(configJSON)
		const caps = parsed?.agent?.capabilities
		if (!Array.isArray(caps)) return []
		return caps.filter((c): c is string => typeof c === "string")
	} catch {
		return []
	}
}

// parseContextsFromConfig extracts the agent's CONTEXT declarations
// from the JSON config blob — each one is a markdown file baked into
// the image at /etc/context/<name>.md and loaded into the model's
// system prompt at runtime. Same tolerance posture as
// parseEnvsFromConfig: any parse failure yields an empty array so
// the UI never panics on a malformed config.
function parseContextsFromConfig(configJSON: string): ContextDecl[] {
	try {
		const parsed = JSON.parse(configJSON)
		const contexts = parsed?.agent?.contexts
		if (!Array.isArray(contexts)) return []
		const out: ContextDecl[] = []
		for (const c of contexts) {
			if (typeof c?.name !== "string") continue
			out.push({
				name: c.name,
				description: typeof c.description === "string" ? c.description : undefined,
				content: typeof c.content === "string" ? c.content : undefined,
			})
		}
		return out
	} catch {
		return []
	}
}
