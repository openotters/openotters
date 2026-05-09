"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import {
	ArrowLeft,
	ArrowRight,
	Check,
	ChevronLeft,
	ChevronRight,
	ExternalLink,
	FileCode,
	Library,
	Plus,
	Sparkles,
	Terminal,
	Trash2,
	Variable,
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

# Optional but recommended — surfaces in the image listing + catalog tab.
LABEL org.opencontainers.image.description="A friendly assistant. Keep replies short and direct."
LABEL org.opencontainers.image.source="https://github.com/your-org/your-repo"

CONTEXT SOUL <<EOF
You are a friendly assistant. Keep replies short and direct.
EOF
`

interface SelectedBin {
	name: string
	ref: string
}

interface DeclaredEnv {
	key: string
	value: string
	description: string
}

interface WizardState {
	from: string
	runtime: string
	modelRef: string
	agentName: string
	description: string
	source: string
	soul: string
	bins: SelectedBin[]
	envs: DeclaredEnv[]
}

const blank: WizardState = {
	from: "scratch",
	runtime: DEFAULT_RUNTIME,
	modelRef: "",
	agentName: "",
	description: "",
	source: "",
	soul: SOUL_TEMPLATE,
	bins: [],
	envs: [],
}

// Reserved ENV keys mirror agentfile/spec/parse.go's reservedEnvKeys
// + the *_API_KEY / *_API_BASE suffix rule. Validated client-side so
// the user gets feedback before BuildAgent rejects the file.
const RESERVED_ENV_KEYS = new Set([
	"PATH",
	"HOME",
	"XDG_CONFIG_HOME",
	"XDG_CACHE_HOME",
	"XDG_DATA_HOME",
	"TMPDIR",
	"LANG",
	"OTTERS_AGENT_ROOT",
])

const ENV_KEY_PATTERN = /^[A-Z_][A-Z0-9_]*$/

function validateEnvKey(key: string): string | null {
	if (key === "") return null // empty rows are draft, not errors
	if (!ENV_KEY_PATTERN.test(key)) {
		return "Uppercase letters, digits, underscore; cannot start with a digit"
	}
	if (RESERVED_ENV_KEYS.has(key)) {
		return "Reserved by the locked-down agent env"
	}
	if (key.endsWith("_API_KEY") || key.endsWith("_API_BASE")) {
		return "_API_KEY / _API_BASE are reserved for provider creds"
	}
	return null
}

// escapeEnvValue quotes ENV values that contain whitespace or quotes
// so the generated Agentfile parses cleanly. Bare alphanumeric +
// punctuation stays unquoted to match how users typically write them.
function escapeEnvValue(v: string): string {
	if (v === "") return '""'
	if (/[\s"\\]/.test(v)) {
		return `"${v.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`
	}
	return v
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
// escapeLabelValue makes a string safe to embed in `LABEL key="value"`.
// Newlines become `\n`, double-quotes get backslash-escaped. Keeps the
// generated Agentfile parseable regardless of what the user typed.
function escapeLabelValue(v: string): string {
	return v.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\n/g, "\\n")
}

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

	const description = state.description.trim()
	const source = state.source.trim()
	if (description || source) {
		lines.push("")
		if (description) {
			lines.push(`LABEL org.opencontainers.image.description="${escapeLabelValue(description)}"`)
		}
		if (source) {
			lines.push(`LABEL org.opencontainers.image.source="${escapeLabelValue(source)}"`)
		}
	}

	const soul = state.soul.trim()
	if (soul) {
		lines.push("", "CONTEXT SOUL <<EOF", soul, "EOF")
	}

	const validEnvs = state.envs.filter((e) => e.key !== "" && validateEnvKey(e.key) === null)
	if (validEnvs.length > 0) {
		lines.push("")
		for (const env of validEnvs) {
			let line = `ENV ${env.key}=${escapeEnvValue(env.value)}`
			const desc = env.description.trim()
			if (desc) {
				line += ` "${desc.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`
			}
			lines.push(line)
		}
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
	{ id: "envs", title: "Env", description: "OS env vars on the agent process" },
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

type CreateTab = "wizard" | "editor" | "catalog"

export default function NewAgentPage() {
	const router = useRouter()
	const [tab, setTab] = useState<CreateTab>("wizard")

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

	const addEnv = () =>
		setState((s) => ({ ...s, envs: [...s.envs, { key: "", value: "", description: "" }] }))

	const updateEnv = (idx: number, patch: Partial<DeclaredEnv>) =>
		setState((s) => ({
			...s,
			envs: s.envs.map((e, i) => (i === idx ? { ...e, ...patch } : e)),
		}))

	const removeEnv = (idx: number) =>
		setState((s) => ({ ...s, envs: s.envs.filter((_, i) => i !== idx) }))

	const wizardAgentfile = useMemo(() => buildAgentfile(state), [state])

	const canAdvanceFromBasics = state.modelRef.trim() !== "" && state.agentName.trim() !== ""

	const stepValid = (): boolean => {
		switch (step) {
			case 0:
				return canAdvanceFromBasics
			case 1:
				return state.soul.trim() !== ""
			case 2:
				return true
			case 3:
				// Block "Next" if any non-empty env row carries an
				// invalid key (reserved, wrong shape, _API_* suffix).
				// Empty rows are drafts and skipped at build time.
				return state.envs.every((e) => e.key === "" || validateEnvKey(e.key) === null)
			case 4:
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

			<Tabs onValueChange={(v) => setTab(v as CreateTab)} value={tab}>
				<TabsList className="grid w-full max-w-xl grid-cols-3">
					<TabsTrigger value="wizard">
						<Sparkles className="mr-2 h-4 w-4" />
						Wizard
					</TabsTrigger>
					<TabsTrigger value="editor">
						<FileCode className="mr-2 h-4 w-4" />
						Editor
					</TabsTrigger>
					<TabsTrigger value="catalog">
						<Library className="mr-2 h-4 w-4" />
						From catalog
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
							{step === 3 && (
								<EnvsStep
									envs={state.envs}
									onAdd={addEnv}
									onRemove={removeEnv}
									onUpdate={updateEnv}
								/>
							)}
							{step === 4 && <ReviewStep agentfile={wizardAgentfile} state={state} />}

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

				<TabsContent className="space-y-6 pt-6" value="catalog">
					<CatalogTab
						error={error}
						images={images.data?.images ?? []}
						imagesLoading={images.isLoading}
						isPending={create.isPending}
						models={models.data?.models ?? []}
						modelsLoading={models.isLoading}
						onSubmit={async (req) => {
							setError(null)
							try {
								await create.mutateAsync(req)
								router.push("/agents")
							} catch (err) {
								setError(err instanceof Error ? err.message : String(err))
							}
						}}
					/>
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

			<Card>
				<CardHeader>
					<CardTitle>Description &amp; source (optional)</CardTitle>
					<CardDescription>
						Stamped on the OCI manifest as{" "}
						<code className="font-mono text-xs">org.opencontainers.image.description</code> and{" "}
						<code className="font-mono text-xs">…source</code>. Surface in the image listing and
						the catalog tab so other people can find this agent later.
					</CardDescription>
				</CardHeader>
				<CardContent className="space-y-3">
					<div className="space-y-1.5">
						<Label className="text-xs">Description</Label>
						<Input
							onChange={(e) => onChange({ description: e.target.value })}
							placeholder="Short blurb — what this agent does, in one sentence."
							value={state.description}
						/>
					</div>
					<div className="space-y-1.5">
						<Label className="text-xs">Source URL</Label>
						<Input
							onChange={(e) => onChange({ source: e.target.value })}
							placeholder="https://github.com/your-org/your-repo"
							value={state.source}
						/>
					</div>
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

interface EnvsStepProps {
	envs: DeclaredEnv[]
	onAdd: () => void
	onUpdate: (idx: number, patch: Partial<DeclaredEnv>) => void
	onRemove: (idx: number) => void
}

function EnvsStep({ envs, onAdd, onUpdate, onRemove }: EnvsStepProps) {
	return (
		<Card>
			<CardHeader>
				<CardTitle>ENV</CardTitle>
				<CardDescription>
					OS environment variables set on the spawned agent process — application
					knobs the agent (or its tools) read via{" "}
					<code className="font-mono text-xs">os.Getenv</code>. Distinct from{" "}
					<code className="font-mono text-xs">CONFIG</code> (a runtime-SDK knob)
					and <code className="font-mono text-xs">ARG</code> (build-time
					substitution). Keys must be uppercase POSIX-style; reserved keys
					(<code className="font-mono text-xs">PATH</code>,{" "}
					<code className="font-mono text-xs">HOME</code>,{" "}
					<code className="font-mono text-xs">XDG_*</code>,{" "}
					<code className="font-mono text-xs">TMPDIR</code>,{" "}
					<code className="font-mono text-xs">LANG</code>,{" "}
					<code className="font-mono text-xs">OTTERS_AGENT_ROOT</code>, plus{" "}
					<code className="font-mono text-xs">*_API_KEY</code> /{" "}
					<code className="font-mono text-xs">*_API_BASE</code>) are rejected to
					keep the locked-down sandbox intact.
				</CardDescription>
			</CardHeader>
			<CardContent className="space-y-3">
				{envs.length === 0 && (
					<p className="rounded-lg border border-dashed p-6 text-center text-muted-foreground text-sm">
						No environment variables. Most agents don't need any —{" "}
						provider creds (<code className="font-mono text-xs">ANTHROPIC_API_KEY</code>{" "}
						etc.) flow through automatically based on the chosen MODEL.
					</p>
				)}
				{envs.map((env, idx) => {
					const keyError = validateEnvKey(env.key)
					return (
						<div
							className={cn(
								"rounded-lg border p-3 space-y-2",
								keyError && env.key !== "" && "border-destructive/40 bg-destructive/5",
							)}
							key={idx}>
							<div className="flex items-start gap-2">
								<Variable className="mt-2 h-4 w-4 shrink-0 text-primary" />
								<div className="flex-1 space-y-2">
									<div className="grid gap-2 sm:grid-cols-[1fr_1fr]">
										<div className="space-y-1">
											<Label className="text-xs">Key</Label>
											<Input
												className="font-mono text-sm"
												onChange={(e) =>
													onUpdate(idx, {
														key: e.target.value
															.toUpperCase()
															.replace(/[^A-Z0-9_]/g, "_"),
													})
												}
												placeholder="NODE_ENV"
												value={env.key}
											/>
											{keyError && env.key !== "" && (
												<p className="text-destructive text-xs">{keyError}</p>
											)}
										</div>
										<div className="space-y-1">
											<Label className="text-xs">Value</Label>
											<Input
												className="font-mono text-sm"
												onChange={(e) => onUpdate(idx, { value: e.target.value })}
												placeholder="production"
												value={env.value}
											/>
										</div>
									</div>
									<div className="space-y-1">
										<Label className="text-xs">Description (optional)</Label>
										<Input
											className="text-sm"
											onChange={(e) => onUpdate(idx, { description: e.target.value })}
											placeholder="Why this is set, what reads it"
											value={env.description}
										/>
									</div>
								</div>
								<Button
									className="text-muted-foreground hover:text-destructive"
									onClick={() => onRemove(idx)}
									size="icon"
									type="button"
									variant="ghost">
									<Trash2 className="h-4 w-4" />
								</Button>
							</div>
						</div>
					)
				})}
				<Button onClick={onAdd} size="sm" type="button" variant="outline">
					<Plus className="mr-2 h-4 w-4" />
					Add ENV
				</Button>
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
					<code className="font-mono text-xs">BuildAgent</code> and built in memory — no file
					lands on the daemon's disk.
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
					<Row label="Description" value={state.description} />
					<Separator />
					<Row label="Source" value={state.source} />
					<Separator />
					<Row label="Tools" value={state.bins.length === 0 ? "none" : `${state.bins.length} bin(s)`} />
					<Separator />
					<Row
						label="Env"
						value={(() => {
							const valid = state.envs.filter(
								(e) => e.key !== "" && validateEnvKey(e.key) === null,
							)
							return valid.length === 0 ? "none" : `${valid.length} var(s)`
						})()}
					/>
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
				{state.envs.some((e) => e.key !== "" && validateEnvKey(e.key) === null) && (
					<div className="flex flex-wrap gap-1">
						{state.envs
							.filter((e) => e.key !== "" && validateEnvKey(e.key) === null)
							.map((e) => (
								<Badge className="font-mono text-xs" key={e.key} variant="secondary">
									{e.key}={e.value || ""}
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

interface CatalogImage {
	ref: string
	digest: string
	artifactType: string
	description: string
	source: string
	createdAt: bigint
	size: bigint
}

interface CatalogModel {
	ref: string
	displayName: string
	provider: string
	name: string
}

interface CatalogTabProps {
	images: CatalogImage[]
	imagesLoading: boolean
	models: CatalogModel[]
	modelsLoading: boolean
	error: string | null
	isPending: boolean
	onSubmit: (req: { ref: string; name?: string; model?: string }) => Promise<void>
}

function CatalogTab({
	images,
	imagesLoading,
	models,
	modelsLoading,
	error,
	isPending,
	onSubmit,
}: CatalogTabProps) {
	const [selectedRef, setSelectedRef] = useState<string | null>(null)
	const [name, setName] = useState("")
	const [model, setModel] = useState("")
	const [search, setSearch] = useState("")

	// Bin artifacts aren't valid agent images — same filter as Images page.
	const agentImages = useMemo(
		() => images.filter((i) => i.artifactType !== BIN_ARTIFACT_TYPE),
		[images],
	)

	const filtered = useMemo(() => {
		const q = search.trim().toLowerCase()
		if (!q) return agentImages
		return agentImages.filter(
			(i) =>
				i.ref.toLowerCase().includes(q) ||
				i.description.toLowerCase().includes(q),
		)
	}, [agentImages, search])

	const selected = filtered.find((i) => i.ref === selectedRef) ?? null

	const handleSubmit = async () => {
		if (!selected) return
		await onSubmit({
			ref: selected.ref,
			name: name.trim() || undefined,
			model: model.trim() || undefined,
		})
	}

	return (
		<div className="grid gap-6 lg:grid-cols-[1fr_360px]">
			<Card>
				<CardHeader>
					<CardTitle>Pick an existing image</CardTitle>
					<CardDescription>
						Skips the build step entirely — the daemon's{" "}
						<code className="font-mono text-xs">CreateAgent</code> RPC creates an instance from a
						pre-built image. Override the name and model below if you want to deviate from the
						image's defaults.
					</CardDescription>
				</CardHeader>
				<CardContent className="space-y-3">
					<Input
						onChange={(e) => setSearch(e.target.value)}
						placeholder="Search by ref or description…"
						value={search}
					/>
					{imagesLoading && <p className="text-muted-foreground text-sm">Loading images…</p>}
					{!imagesLoading && filtered.length === 0 && (
						<p className="rounded-lg border border-dashed p-6 text-center text-muted-foreground text-sm">
							{search
								? "No images match the search."
								: "No agent images in the registry. Build one first via the Wizard or Editor tab."}
						</p>
					)}
					<div className="space-y-2">
						{filtered.map((image) => {
							const isSel = image.ref === selectedRef
							return (
								<button
									className={cn(
										"w-full rounded-lg border p-3 text-left transition-colors hover:bg-muted/50",
										isSel && "border-primary bg-primary/5",
									)}
									key={image.digest}
									onClick={() => setSelectedRef(image.ref)}
									type="button">
									<div className="flex items-start justify-between gap-3">
										<div className="min-w-0 flex-1">
											<p className="truncate font-mono font-medium text-sm">{image.ref}</p>
											{image.description && (
												<p className="mt-1 line-clamp-2 text-muted-foreground text-sm">
													{image.description}
												</p>
											)}
											{image.source && (
												<a
													className="mt-1 inline-flex items-center gap-1 text-muted-foreground text-xs underline-offset-2 hover:text-foreground hover:underline"
													href={image.source}
													onClick={(e) => e.stopPropagation()}
													rel="noreferrer"
													target="_blank">
													<ExternalLink className="h-3 w-3" />
													<span className="max-w-[40ch] truncate">{image.source}</span>
												</a>
											)}
										</div>
										{isSel && (
											<Check className="mt-1 h-4 w-4 shrink-0 text-primary" />
										)}
									</div>
								</button>
							)
						})}
					</div>
				</CardContent>
			</Card>

			<div className="space-y-4">
				<Card className="sticky top-6">
					<CardHeader className="pb-2">
						<CardTitle className="text-base">Run options</CardTitle>
						<CardDescription className="text-xs">
							Both fields are optional — leave blank to take the image's declared values.
						</CardDescription>
					</CardHeader>
					<CardContent className="space-y-4">
						<div className="space-y-2">
							<Label className="text-xs">Selected image</Label>
							<p className="break-all font-mono text-sm">
								{selected ? selected.ref : <span className="text-muted-foreground">— none —</span>}
							</p>
						</div>
						<div className="space-y-2">
							<Label className="text-xs">Name override</Label>
							<Input
								disabled={!selected}
								onChange={(e) =>
									setName(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "-"))
								}
								placeholder="auto-generated"
								value={name}
							/>
						</div>
						<div className="space-y-2">
							<Label className="text-xs">Model override</Label>
							<Select disabled={!selected || modelsLoading} onValueChange={setModel} value={model}>
								<SelectTrigger>
									<SelectValue
										placeholder={
											modelsLoading
												? "Loading…"
												: models.length === 0
													? "No models configured"
													: "use image default"
										}
									/>
								</SelectTrigger>
								<SelectContent>
									{models.map((m) => (
										<SelectItem key={m.ref} value={m.ref}>
											<span className="font-mono">{m.ref}</span>
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
						{error && <p className="text-destructive text-xs">{error}</p>}
						<div className="flex gap-2 pt-2">
							<Button asChild className="flex-1" variant="outline">
								<Link href="/agents">Cancel</Link>
							</Button>
							<Button
								className="flex-1"
								disabled={!selected || isPending}
								onClick={handleSubmit}>
								{isPending ? "Working…" : "Run"}
							</Button>
						</div>
					</CardContent>
				</Card>
			</div>
		</div>
	)
}
