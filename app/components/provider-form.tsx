"use client"

import { useMutation } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { Check, Copy, Eye, EyeOff, Plus, Trash2 } from "lucide-react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useState } from "react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Separator } from "@/components/ui/separator"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { Provider } from "@/lib/proto/v1/daemon_pb"
import { addProvider, updateProvider } from "@/lib/proto/v1/daemon-Runtime_connectquery"

export interface ProviderFormValue {
	name: string
	apiBase: string
	apiKey: string
	apiKeyIsEnvVar: boolean
	models: string[]
}

interface ProviderFormProps {
	mode: "create" | "edit"
	initial?: Provider
}

const blank: ProviderFormValue = {
	name: "",
	apiBase: "",
	apiKey: "",
	apiKeyIsEnvVar: true,
	models: [],
}

function fromProvider(p: Provider): ProviderFormValue {
	const apiKey = p.apiKey ?? ""
	return {
		name: p.name,
		apiBase: p.apiBase ?? "",
		apiKey,
		// Treat literal "${VAR}" markers as env-var references; daemon
		// expands those at load time. Anything else is the literal key.
		apiKeyIsEnvVar: apiKey.startsWith("${"),
		models: [...p.models],
	}
}

function generateYaml(value: ProviderFormValue): string {
	const lines: string[] = ["providers:"]
	const name = value.name || "<name>"
	lines.push(`  - name: ${name}`)
	if (value.apiBase) {
		lines.push(`    api-base: ${value.apiBase}`)
	}
	lines.push(`    api-key: ${value.apiKey || "<secret>"}`)
	if (value.models.length > 0) {
		lines.push(`    models:`)
		for (const m of value.models) {
			lines.push(`      - ${m}`)
		}
	}
	return lines.join("\n")
}

export function ProviderForm({ mode, initial }: ProviderFormProps) {
	const router = useRouter()
	const queryClient = useQueryClient()
	const [value, setValue] = useState<ProviderFormValue>(initial ? fromProvider(initial) : blank)
	const [showKey, setShowKey] = useState(false)
	const [copied, setCopied] = useState(false)
	const [submitError, setSubmitError] = useState<string | null>(null)

	// One mutation per mode; the form picks the right one in handleSave.
	// Both invalidate the providers list on success so /providers
	// reflects the change without a manual refresh.
	const onSuccess = () => {
		queryClient.invalidateQueries({ queryKey: ["openotters.daemon.v1.Runtime", "ListProviders"] })
		router.push("/providers")
	}
	const create = useMutation(addProvider, { onSuccess })
	const updateMut = useMutation(updateProvider, { onSuccess })

	const update = (patch: Partial<ProviderFormValue>) => setValue((v) => ({ ...v, ...patch }))

	const yaml = generateYaml(value)

	const handleCopy = async () => {
		await navigator.clipboard.writeText(yaml)
		setCopied(true)
		setTimeout(() => setCopied(false), 1500)
	}

	const buildPayload = (): Provider => ({
		$typeName: "openotters.daemon.v1.Provider",
		name: value.name.trim(),
		apiKey: value.apiKey.trim(),
		apiBase: value.apiBase.trim(),
		models: value.models.map((m) => m.trim()).filter(Boolean),
	})

	const handleSave = () => {
		setSubmitError(null)
		const payload = buildPayload()
		if (!payload.name) {
			setSubmitError("name is required")
			return
		}

		const args = { provider: payload }
		const mutation = mode === "create" ? create : updateMut
		mutation.mutate(args, {
			onError: (err) => setSubmitError(err.message),
		})
	}

	const addModel = () => update({ models: [...value.models, ""] })

	const updateModel = (index: number, next: string) => {
		const arr = [...value.models]
		arr[index] = next
		update({ models: arr })
	}

	const removeModel = (index: number) => {
		update({ models: value.models.filter((_, i) => i !== index) })
	}

	const isSaveDisabled = !value.name || create.isPending || updateMut.isPending

	return (
		<div className="grid gap-6 lg:grid-cols-[1fr_450px]">
			<div className="space-y-6">
				<Tabs className="w-full" defaultValue="connection">
					<TabsList className="grid w-full grid-cols-3">
						<TabsTrigger value="connection">Connection</TabsTrigger>
						<TabsTrigger value="auth">Authentication</TabsTrigger>
						<TabsTrigger value="models">Models</TabsTrigger>
					</TabsList>

					<TabsContent className="space-y-4 pt-4" value="connection">
						<Card>
							<CardHeader>
								<CardTitle>Identifier</CardTitle>
								<CardDescription>
									Lowercase slug used in <code className="font-mono">MODEL provider/model</code>{" "}
									references.
								</CardDescription>
							</CardHeader>
							<CardContent>
								<Input
									disabled={mode === "edit"}
									onChange={(e) =>
										update({ name: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "-") })
									}
									placeholder="anthropic"
									value={value.name}
								/>
							</CardContent>
						</Card>

						<Card>
							<CardHeader>
								<CardTitle>API base URL</CardTitle>
								<CardDescription>
									Optional. Override only for custom endpoints (Ollama, proxies, gateways).
								</CardDescription>
							</CardHeader>
							<CardContent>
								<Input
									onChange={(e) => update({ apiBase: e.target.value })}
									placeholder="https://api.anthropic.com"
									value={value.apiBase}
								/>
							</CardContent>
						</Card>
					</TabsContent>

					<TabsContent className="space-y-4 pt-4" value="auth">
						<Card>
							<CardHeader>
								<CardTitle>API key</CardTitle>
								<CardDescription>
									Stored on the daemon host in{" "}
									<code className="font-mono text-xs">~/.otters/providers.yaml</code>. Never embedded
									in agent artifacts.
								</CardDescription>
							</CardHeader>
							<CardContent className="space-y-4">
								<div className="flex items-center gap-3">
									<Switch
										checked={value.apiKeyIsEnvVar}
										onCheckedChange={(checked) => update({ apiKeyIsEnvVar: checked })}
									/>
									<Label
										className="cursor-pointer"
										onClick={() => update({ apiKeyIsEnvVar: !value.apiKeyIsEnvVar })}>
										Reference an environment variable
									</Label>
								</div>
								<div className="space-y-2">
									<Label>{value.apiKeyIsEnvVar ? "Variable expression" : "API key"}</Label>
									<div className="relative">
										<Input
											className="pr-10 font-mono"
											onChange={(e) => update({ apiKey: e.target.value })}
											placeholder={value.apiKeyIsEnvVar ? "${ANTHROPIC_API_KEY}" : "sk-ant-..."}
											type={value.apiKeyIsEnvVar || showKey ? "text" : "password"}
											value={value.apiKey}
										/>
										{!value.apiKeyIsEnvVar && (
											<Button
												aria-label={showKey ? "Hide key" : "Show key"}
												className="-translate-y-1/2 absolute top-1/2 right-1 h-7 w-7"
												onClick={() => setShowKey((v) => !v)}
												size="icon"
												type="button"
												variant="ghost">
												{showKey ? (
													<EyeOff className="h-3.5 w-3.5" />
												) : (
													<Eye className="h-3.5 w-3.5" />
												)}
											</Button>
										)}
									</div>
									<p className="text-muted-foreground text-xs">
										{value.apiKeyIsEnvVar ? (
											<>Expanded at daemon startup. Recommended.</>
										) : (
											<>The literal value is written to the config.</>
										)}
									</p>
								</div>
							</CardContent>
						</Card>
					</TabsContent>

					<TabsContent className="space-y-4 pt-4" value="models">
						<Card>
							<CardHeader className="flex flex-row items-center justify-between space-y-0">
								<div>
									<CardTitle>Allow-list</CardTitle>
									<CardDescription>
										Only models named here are allowed via <code className="font-mono">MODEL</code>.
										Empty = allow any model the upstream provider serves.
									</CardDescription>
								</div>
								<Button onClick={addModel} size="sm" variant="outline">
									<Plus className="mr-2 h-4 w-4" />
									Add Model
								</Button>
							</CardHeader>
							<CardContent className="space-y-3">
								{value.models.length === 0 ? (
									<div className="rounded-lg border border-dashed py-8 text-center text-muted-foreground text-sm">
										No models in the allow-list — every model the provider serves is allowed.
									</div>
								) : (
									value.models.map((model, index) => (
										<div className="flex items-center gap-2" key={index}>
											<Input
												className="flex-1 font-mono"
												onChange={(e) => updateModel(index, e.target.value)}
												placeholder="claude-haiku-4-5-20251001"
												value={model}
											/>
											<Button
												onClick={() => removeModel(index)}
												size="icon"
												type="button"
												variant="ghost">
												<Trash2 className="h-4 w-4" />
											</Button>
										</div>
									))
								)}
							</CardContent>
						</Card>
					</TabsContent>
				</Tabs>
			</div>

			<div className="space-y-4">
				<Card className="sticky top-6">
					<CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
						<div>
							<CardTitle className="text-base">providers.yaml</CardTitle>
							<CardDescription className="text-xs">Daemon config preview</CardDescription>
						</div>
						<Button onClick={handleCopy} size="sm" variant="ghost">
							{copied ? <Check className="mr-2 h-4 w-4" /> : <Copy className="mr-2 h-4 w-4" />}
							{copied ? "Copied" : "Copy"}
						</Button>
					</CardHeader>
					<CardContent>
						<ScrollArea className="h-[260px]">
							<pre className="whitespace-pre-wrap rounded-lg bg-muted p-4 font-mono text-xs">
								<code>{yaml}</code>
							</pre>
						</ScrollArea>
					</CardContent>
				</Card>

				<Card>
					<CardHeader>
						<CardTitle className="text-base">Status</CardTitle>
					</CardHeader>
					<CardContent className="space-y-2 text-sm">
						<div className="flex items-center justify-between">
							<span className="text-muted-foreground">Mode</span>
							<Badge variant="secondary">{mode === "create" ? "Adding" : "Editing"}</Badge>
						</div>
						<Separator />
						<p className="text-muted-foreground text-xs">
							Saving sends a Connect RPC to <code className="font-mono">ottersd</code>. The daemon
							writes the new provider to <code className="font-mono">~/.otters/providers.yaml</code>{" "}
							and picks up the change on its next provider lookup.
						</p>
						{submitError && (
							<>
								<Separator />
								<p className="text-destructive text-xs">{submitError}</p>
							</>
						)}
					</CardContent>
				</Card>

				<div className="flex gap-2">
					<Button asChild className="flex-1" variant="outline">
						<Link href="/providers">Cancel</Link>
					</Button>
					<Button className="flex-1" disabled={isSaveDisabled} onClick={handleSave}>
						{create.isPending || updateMut.isPending
							? "Saving…"
							: mode === "create"
								? "Add Provider"
								: "Save changes"}
					</Button>
				</div>
			</div>
		</div>
	)
}
