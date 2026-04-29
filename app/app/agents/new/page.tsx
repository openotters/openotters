"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import {
	ArrowLeft,
	ArrowRight,
	Check,
	ChevronLeft,
	ChevronRight,
	FileCode,
	Sparkles,
	Terminal,
} from "lucide-react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useMemo, useState } from "react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { cn } from "@/lib/utils"
import {
	buildAgent,
	createAgent,
	listImages,
	listModels,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"

const BIN_ARTIFACT_TYPE = "application/vnd.openotters.bin.v1"
const DEFAULT_RUNTIME = "ghcr.io/openotters/runtime:latest"

const SOUL_TEMPLATE = `You are a friendly assistant. Keep replies short and direct.`

const EDITOR_TEMPLATE = `# syntax=openotters/agentfile:1
FROM scratch

RUNTIME ghcr.io/openotters/runtime:latest
MODEL anthropic/claude-haiku-4-5-20251001
NAME hello

CONTEXT SOUL <<EOF
You are a friendly assistant. Keep replies short and direct.
EOF
`

interface SelectedBin {
	name: string
	ref: string
}

interface WizardState {
	from: string
	runtime: string
	modelRef: string
	agentName: string
	soul: string
	bins: SelectedBin[]
}

const blank: WizardState = {
	from: "scratch",
	runtime: DEFAULT_RUNTIME,
	modelRef: "",
	agentName: "",
	soul: SOUL_TEMPLATE,
	bins: [],
}

// shortBinName trims an OCI ref down to a usable BIN alias. Reference
// shapes vary widely (`ghcr.io/openotters/tools/jq:latest`,
// `tools/jq:v1`, plain `jq:latest`) so we take the segment before the
// last colon and after the last slash. Stripping common prefix paths
// keeps the alias close to what users would type if writing the
// Agentfile by hand.
function shortBinName(ref: string): string {
	const noTag = ref.split(":")[0] ?? ref
	const tail = noTag.split("/").pop() ?? noTag
	return tail.replace(/[^a-zA-Z0-9_-]/g, "_")
}

// buildAgentfile assembles a valid Agentfile string from the wizard
// state. Mirrors the directives accepted by `agentbuild.FromFile`;
// keeping the formatting consistent (single blank line between
// sections) so the preview reads like a hand-written file.
function buildAgentfile(state: WizardState): string {
	const lines: string[] = ["# syntax=openotters/agentfile:1", "", `FROM ${state.from || "scratch"}`, ""]

	if (state.runtime) {
		lines.push(`RUNTIME ${state.runtime}`)
	}
	if (state.modelRef) {
		lines.push(`MODEL ${state.modelRef}`)
	}
	if (state.agentName) {
		lines.push(`NAME ${state.agentName}`)
	}

	const soul = state.soul.trim()
	if (soul) {
		lines.push("", "CONTEXT SOUL <<EOF", soul, "EOF")
	}

	if (state.bins.length > 0) {
		lines.push("")
		for (const bin of state.bins) {
			lines.push(`BIN ${bin.name} ${bin.ref}`)
		}
	}

	return lines.join("\n") + "\n"
}

interface StepDef {
	id: string
	title: string
	description: string
}

const STEPS: StepDef[] = [
	{ id: "basics", title: "Basics", description: "FROM, RUNTIME, MODEL, NAME" },
	{ id: "soul", title: "Soul", description: "Personality & instructions" },
	{ id: "bins", title: "Tools", description: "Pick installed binaries" },
	{ id: "review", title: "Review", description: "Confirm and build" },
]

function Stepper({ current }: { current: number }) {
	return (
		<ol className="flex items-center gap-2">
			{STEPS.map((step, idx) => {
				const done = idx < current
				const active = idx === current
				return (
					<li className="flex flex-1 items-center gap-2" key={step.id}>
						<div
							className={cn(
								"flex h-8 w-8 shrink-0 items-center justify-center rounded-full border font-medium text-xs",
								active && "border-primary bg-primary text-primary-foreground",
								done && "border-emerald-500 bg-emerald-500 text-white",
								!active && !done && "border-muted-foreground/30 text-muted-foreground",
							)}>
							{done ? <Check className="h-4 w-4" /> : idx + 1}
						</div>
						<div className="hidden flex-1 sm:block">
							<p
								className={cn(
									"font-medium text-sm",
									active ? "text-foreground" : "text-muted-foreground",
								)}>
								{step.title}
							</p>
							<p className="text-muted-foreground text-xs">{step.description}</p>
						</div>
						{idx < STEPS.length - 1 && <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />}
					</li>
				)
			})}
		</ol>
	)
}

export default function NewAgentPage() {
	const router = useRouter()
	const [tab, setTab] = useState<"wizard" | "editor">("wizard")

	// Wizard state
	const [state, setState] = useState<WizardState>(blank)
	const [step, setStep] = useState(0)

	// Editor state
	const [editorText, setEditorText] = useState(EDITOR_TEMPLATE)

	// Shared submission state
	const [error, setError] = useState<string | null>(null)
	const build = useMutation(buildAgent)
	const create = useMutation(createAgent)

	const models = useQuery(listModels, {})
	const images = useQuery(listImages, {})

	const baseImageOptions = useMemo(() => {
		const all = images.data?.images ?? []
		// "scratch" is the canonical empty base; surface it first so
		// it's the default for new agents that aren't extending an
		// existing one. Filter out bin artifacts — they're not valid
		// FROM targets.
		const refs = all.filter((i) => i.artifactType !== BIN_ARTIFACT_TYPE).map((i) => i.ref)
		return ["scratch", ...refs]
	}, [images.data])

	const binOptions = useMemo(() => {
		const all = images.data?.images ?? []
		return all.filter((i) => i.artifactType === BIN_ARTIFACT_TYPE)
	}, [images.data])

	const updateState = (patch: Partial<WizardState>) => setState((s) => ({ ...s, ...patch }))

	const toggleBin = (ref: string, checked: boolean) => {
		setState((s) => {
			if (checked) {
				if (s.bins.some((b) => b.ref === ref)) {
					return s
				}
				return { ...s, bins: [...s.bins, { name: shortBinName(ref), ref }] }
			}
			return { ...s, bins: s.bins.filter((b) => b.ref !== ref) }
		})
	}

	const renameBin = (ref: string, name: string) => {
		setState((s) => ({
			...s,
			bins: s.bins.map((b) => (b.ref === ref ? { ...b, name } : b)),
		}))
	}

	const wizardAgentfile = useMemo(() => buildAgentfile(state), [state])

	const canAdvanceFromBasics = state.modelRef.trim() !== "" && state.agentName.trim() !== ""

	const stepValid = (): boolean => {
		switch (step) {
			case 0:
				return canAdvanceFromBasics
			case 1:
				return state.soul.trim() !== ""
			case 2:
			case 3:
				return true
			default:
				return false
		}
	}

	const submit = async (agentfileText: string) => {
		setError(null)
		const text = agentfileText.trim()
		if (!text) {
			setError("Agentfile content is empty")
			return
		}

		try {
			const buildResp = await build.mutateAsync({
				content: new TextEncoder().encode(text),
			})
			const ref = buildResp.tags[0] ?? buildResp.ref
			if (!ref) {
				setError("Build produced no tags")
				return
			}

			await create.mutateAsync({ ref })

			router.push("/agents")
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err))
		}
	}

	const isPending = build.isPending || create.isPending

	return (
		<div className="space-y-6">
			<div className="flex items-center gap-4">
				<Button asChild size="icon" variant="ghost">
					<Link href="/agents">
						<ArrowLeft className="h-4 w-4" />
					</Link>
				</Button>
				<div>
					<h1 className="font-semibold text-2xl tracking-tight">Create Agent</h1>
					<p className="text-muted-foreground">
						Compose a fresh agent. Build runs server-side via{" "}
						<code className="font-mono text-xs">BuildAgent</code>; the agent starts via{" "}
						<code className="font-mono text-xs">CreateAgent</code>.
					</p>
				</div>
			</div>

			<Tabs onValueChange={(v) => setTab(v as "wizard" | "editor")} value={tab}>
				<TabsList className="grid w-full max-w-sm grid-cols-2">
					<TabsTrigger value="wizard">
						<Sparkles className="mr-2 h-4 w-4" />
						Wizard
					</TabsTrigger>
					<TabsTrigger value="editor">
						<FileCode className="mr-2 h-4 w-4" />
						Editor
					</TabsTrigger>
				</TabsList>

				<TabsContent className="space-y-6 pt-6" value="wizard">
					<Card>
						<CardContent className="py-4">
							<Stepper current={step} />
						</CardContent>
					</Card>

					<div className="grid gap-6 lg:grid-cols-[1fr_360px]">
						<div className="space-y-4">
							{step === 0 && (
								<BasicsStep
									baseImages={baseImageOptions}
									models={models.data?.models ?? []}
									modelsLoading={models.isLoading}
									onChange={updateState}
									state={state}
								/>
							)}
							{step === 1 && <SoulStep onChange={updateState} state={state} />}
							{step === 2 && (
								<BinsStep
									available={binOptions}
									availableLoading={images.isLoading}
									onRename={renameBin}
									onToggle={toggleBin}
									selected={state.bins}
								/>
							)}
							{step === 3 && <ReviewStep agentfile={wizardAgentfile} state={state} />}

							<div className="flex items-center justify-between">
								<Button
									disabled={step === 0}
									onClick={() => setStep((s) => Math.max(0, s - 1))}
									variant="outline">
									<ChevronLeft className="mr-2 h-4 w-4" />
									Back
								</Button>
								{step < STEPS.length - 1 ? (
									<Button
										disabled={!stepValid()}
										onClick={() => setStep((s) => Math.min(STEPS.length - 1, s + 1))}>
										Next
										<ChevronRight className="ml-2 h-4 w-4" />
									</Button>
								) : (
									<Button disabled={isPending} onClick={() => submit(wizardAgentfile)}>
										{isPending ? "Working…" : "Build & run"}
									</Button>
								)}
							</div>
						</div>

						<AgentfilePreviewPane error={error} mode="wizard" text={wizardAgentfile} />
					</div>
				</TabsContent>

				<TabsContent className="space-y-6 pt-6" value="editor">
					<div className="grid gap-6 lg:grid-cols-[1fr_360px]">
						<Card>
							<CardHeader className="flex flex-row items-center justify-between space-y-0">
								<div>
									<CardTitle>Agentfile</CardTitle>
									<CardDescription>
										Write the Agentfile directly. Useful when you already know the directives or
										want full control. CONTEXT/ADD <code className="font-mono">file://</code>{" "}
										refs aren't supported in this mode (no daemon-side siblings).
									</CardDescription>
								</div>
								<Button
									onClick={() => setEditorText(EDITOR_TEMPLATE)}
									size="sm"
									variant="outline">
									Reset
								</Button>
							</CardHeader>
							<CardContent>
								<Textarea
									className="min-h-[400px] font-mono text-sm"
									onChange={(e) => setEditorText(e.target.value)}
									placeholder={EDITOR_TEMPLATE}
									spellCheck={false}
									value={editorText}
								/>
							</CardContent>
						</Card>

						<div className="space-y-4">
							<AgentfilePreviewPane error={error} mode="editor" text={editorText} />
							<div className="flex gap-2">
								<Button asChild className="flex-1" variant="outline">
									<Link href="/agents">Cancel</Link>
								</Button>
								<Button
									className="flex-1"
									disabled={isPending || !editorText.trim()}
									onClick={() => submit(editorText)}>
									{isPending ? "Working…" : "Build & run"}
								</Button>
							</div>
						</div>
					</div>
				</TabsContent>
			</Tabs>
		</div>
	)
}

interface BasicsStepProps {
	state: WizardState
	onChange: (patch: Partial<WizardState>) => void
	baseImages: string[]
	models: { ref: string; displayName: string; provider: string; name: string }[]
	modelsLoading: boolean
}

function BasicsStep({ state, onChange, baseImages, models, modelsLoading }: BasicsStepProps) {
	return (
		<>
			<Card>
				<CardHeader>
					<CardTitle>FROM</CardTitle>
					<CardDescription>
						Base image. <code className="font-mono">scratch</code> for an empty agent, or extend an
						existing image to inherit its contexts and bins.
					</CardDescription>
				</CardHeader>
				<CardContent>
					<Select onValueChange={(v) => onChange({ from: v })} value={state.from}>
						<SelectTrigger>
							<SelectValue placeholder="scratch" />
						</SelectTrigger>
						<SelectContent>
							{baseImages.map((ref) => (
								<SelectItem key={ref} value={ref}>
									{ref}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle>RUNTIME</CardTitle>
					<CardDescription>OCI image carrying the runtime binary that drives the agent.</CardDescription>
				</CardHeader>
				<CardContent>
					<Input
						className="font-mono"
						onChange={(e) => onChange({ runtime: e.target.value })}
						placeholder={DEFAULT_RUNTIME}
						value={state.runtime}
					/>
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle>MODEL</CardTitle>
					<CardDescription>
						LLM the runtime calls each turn. Listed models come from your configured providers — add
						a provider if the dropdown is empty.
					</CardDescription>
				</CardHeader>
				<CardContent>
					<Select
						disabled={modelsLoading || models.length === 0}
						onValueChange={(v) => onChange({ modelRef: v })}
						value={state.modelRef}>
						<SelectTrigger>
							<SelectValue
								placeholder={
									modelsLoading
										? "Loading…"
										: models.length === 0
											? "No models — configure a provider first"
											: "provider/model"
								}
							/>
						</SelectTrigger>
						<SelectContent>
							{models.map((m) => (
								<SelectItem key={m.ref} value={m.ref}>
									<span className="font-mono">{m.ref}</span>
									{m.displayName && (
										<span className="ml-2 text-muted-foreground text-xs">
											— {m.displayName}
										</span>
									)}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle>NAME</CardTitle>
					<CardDescription>
						Agent identity. Used as the image name and (by default) the running instance name. Lowercase
						letters, digits, and hyphens.
					</CardDescription>
				</CardHeader>
				<CardContent>
					<Input
						onChange={(e) =>
							onChange({ agentName: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "-") })
						}
						placeholder="hello"
						value={state.agentName}
					/>
				</CardContent>
			</Card>
		</>
	)
}

interface SoulStepProps {
	state: WizardState
	onChange: (patch: Partial<WizardState>) => void
}

function SoulStep({ state, onChange }: SoulStepProps) {
	return (
		<Card>
			<CardHeader>
				<CardTitle>CONTEXT SOUL</CardTitle>
				<CardDescription>
					The persona / instruction layer. Loaded as the agent's primary context every turn — keep it
					focused so the model has clear marching orders.
				</CardDescription>
			</CardHeader>
			<CardContent>
				<Textarea
					className="min-h-[280px] font-mono text-sm"
					onChange={(e) => onChange({ soul: e.target.value })}
					placeholder={SOUL_TEMPLATE}
					value={state.soul}
				/>
			</CardContent>
		</Card>
	)
}

interface BinsStepProps {
	available: { ref: string; digest: string }[]
	availableLoading: boolean
	selected: SelectedBin[]
	onToggle: (ref: string, checked: boolean) => void
	onRename: (ref: string, name: string) => void
}

function BinsStep({ available, availableLoading, selected, onToggle, onRename }: BinsStepProps) {
	const isSelected = (ref: string) => selected.some((b) => b.ref === ref)

	return (
		<Card>
			<CardHeader>
				<CardTitle>BIN</CardTitle>
				<CardDescription>
					Tools the agent can call. Each selection becomes a{" "}
					<code className="font-mono text-xs">BIN &lt;alias&gt; &lt;ref&gt;</code> directive. Pull more
					into the registry from the <Link className="underline" href="/bins">Bins</Link> page.
				</CardDescription>
			</CardHeader>
			<CardContent>
				{availableLoading && <p className="text-muted-foreground text-sm">Loading bins…</p>}
				{!availableLoading && available.length === 0 && (
					<p className="text-muted-foreground text-sm">
						No bin images in the registry. Use{" "}
						<code className="font-mono text-xs">otters bin pull &lt;ref&gt;</code> first.
					</p>
				)}
				<div className="space-y-2">
					{available.map((image) => {
						const checked = isSelected(image.ref)
						const sel = selected.find((b) => b.ref === image.ref)
						return (
							<label
								className={cn(
									"flex cursor-pointer items-center gap-3 rounded-lg border p-3 transition-colors hover:bg-muted/50",
									checked && "border-primary bg-primary/5",
								)}
								key={image.ref}>
								<Checkbox
									checked={checked}
									onCheckedChange={(c) => onToggle(image.ref, c === true)}
								/>
								<Terminal className="h-4 w-4 shrink-0 text-primary" />
								<div className="flex-1 space-y-1">
									<p className="font-mono text-sm">{image.ref}</p>
									{checked && (
										<div className="flex items-center gap-2">
											<Label className="text-muted-foreground text-xs">alias</Label>
											<Input
												className="h-7 font-mono text-xs"
												onChange={(e) => onRename(image.ref, e.target.value)}
												value={sel?.name ?? ""}
											/>
										</div>
									)}
								</div>
							</label>
						)
					})}
				</div>
			</CardContent>
		</Card>
	)
}

interface ReviewStepProps {
	state: WizardState
	agentfile: string
}

function ReviewStep({ state, agentfile }: ReviewStepProps) {
	return (
		<Card>
			<CardHeader>
				<CardTitle>Review</CardTitle>
				<CardDescription>
					Confirm and build. The Agentfile content is sent inline to{" "}
					<code className="font-mono text-xs">BuildAgent</code>; the daemon writes it to a temp file
					and runs the OCI build pipeline.
				</CardDescription>
			</CardHeader>
			<CardContent className="space-y-4 text-sm">
				<div className="space-y-2">
					<Row label="FROM" value={state.from} />
					<Separator />
					<Row label="RUNTIME" value={state.runtime} />
					<Separator />
					<Row label="MODEL" value={state.modelRef} />
					<Separator />
					<Row label="NAME" value={state.agentName} />
					<Separator />
					<Row label="Tools" value={state.bins.length === 0 ? "none" : `${state.bins.length} bin(s)`} />
				</div>
				{state.bins.length > 0 && (
					<div className="flex flex-wrap gap-1">
						{state.bins.map((b) => (
							<Badge className="font-mono text-xs" key={b.ref} variant="secondary">
								{b.name}
							</Badge>
						))}
					</div>
				)}
				<details>
					<summary className="cursor-pointer font-medium text-xs text-muted-foreground">
						Show Agentfile preview
					</summary>
					<ScrollArea className="mt-2 h-[240px]">
						<pre className="whitespace-pre-wrap rounded-lg bg-muted p-4 font-mono text-xs">
							<code>{agentfile}</code>
						</pre>
					</ScrollArea>
				</details>
			</CardContent>
		</Card>
	)
}

function Row({ label, value }: { label: string; value: string }) {
	return (
		<div className="flex justify-between gap-4">
			<span className="text-muted-foreground">{label}</span>
			<span className="break-all text-right font-mono text-xs">{value || "—"}</span>
		</div>
	)
}

interface PreviewPaneProps {
	text: string
	error: string | null
	mode: "wizard" | "editor"
}

function AgentfilePreviewPane({ text, error, mode }: PreviewPaneProps) {
	return (
		<Card className="sticky top-6">
			<CardHeader className="pb-2">
				<CardTitle className="text-base">Agentfile preview</CardTitle>
				<CardDescription className="text-xs">
					{mode === "wizard"
						? "Generated from your wizard answers — kept in sync as you edit."
						: "Sent verbatim to the daemon as inline content."}
				</CardDescription>
			</CardHeader>
			<CardContent>
				<ScrollArea className="h-[400px]">
					<pre className="whitespace-pre-wrap rounded-lg bg-muted p-4 font-mono text-xs">
						<code>{text}</code>
					</pre>
				</ScrollArea>
				{error && <p className="mt-2 text-destructive text-xs">{error}</p>}
			</CardContent>
		</Card>
	)
}
