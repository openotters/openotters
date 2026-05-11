"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { Play, Plus, Trash2 } from "lucide-react"
import { useRouter } from "next/navigation"
import { type ReactNode, useEffect, useMemo, useState } from "react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
	DialogTrigger,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import {
	createAgent,
	listModels,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"

// ImageEnv mirrors the per-env subset of the parsed image config —
// re-declared here so the dialog stays decoupled from the image
// detail view's own parser. Caller passes the already-extracted
// envs in; empty array is the "config not surfaced" case
// (docker-backed agents where DescribeImage doesn't return the
// agent spec blob today). The dialog still works in that case —
// the user can add overrides manually via "+ Add".
export interface ImageEnv {
	key: string
	value: string
	description?: string
}

interface RunFromImageDialogProps {
	imageRef: string
	// envs are the ENV directives the image declared (parsed from
	// describe.config when available). Each declared env pre-fills
	// an override input; unset / empty array means the dialog opens
	// with no rows and the user can +Add their own.
	envs: ReadonlyArray<ImageEnv>
	// trigger is the element that opens the dialog. Lets the caller
	// place a Run button anywhere (top of the detail page, inline
	// in a catalog card, etc.) without re-implementing the
	// open/close state plumbing.
	trigger: ReactNode
}

// EnvRow is the dialog's per-row editor state. `key` and `value`
// are user-editable for new rows; for rows seeded from declared
// envs `key` is locked (the row represents an override of a
// specific declared key). `defaultValue` carries the image's
// declared default so the row can detect "changed" and render a
// reset link.
interface EnvRow {
	key: string
	value: string
	description: string
	// origin distinguishes declared (locked key) from custom
	// (user-typed key, can be removed). Declared rows always render
	// alongside the image's spec; custom rows can be deleted.
	origin: "declared" | "custom"
	defaultValue: string
}

// nameFromRef derives a reasonable default agent name from an image
// ref. "ghcr.io/openotters/agents/hass:latest" → "hass-" + 4 random
// chars. The suffix avoids collisions on consecutive runs from the
// same image; the daemon enforces uniqueness anyway, but a fresh
// suffix means the user doesn't have to retype.
function nameFromRef(ref: string): string {
	const lastSlash = ref.lastIndexOf("/")
	const tail = lastSlash === -1 ? ref : ref.slice(lastSlash + 1)
	const base = tail.split(":")[0] || "agent"
	const suffix = Math.random().toString(36).slice(2, 6)
	return `${base}-${suffix}`
}

// RunFromImageDialog launches a fresh agent from `imageRef` with
// per-run env + model overrides on top of whatever the image's
// Agentfile declared. Empty `envs` is supported — the user can
// type custom keys via "+ Add". Empty model override leaves the
// image's declared model in place.
export function RunFromImageDialog({ imageRef, envs, trigger }: RunFromImageDialogProps) {
	const router = useRouter()
	const queryClient = useQueryClient()

	const models = useQuery(listModels, {})

	const [open, setOpen] = useState(false)
	const [name, setName] = useState(() => nameFromRef(imageRef))
	const [model, setModel] = useState("")
	// Both declared rows (from the image's ENV directives) and any
	// custom rows the user added live in the same list, distinguished
	// by `origin`. Declared rows always render at the top and can't
	// be removed; custom rows can be added / removed freely.
	const initialRows = useMemo<EnvRow[]>(
		() =>
			envs.map((e) => ({
				key: e.key,
				value: e.value,
				description: e.description ?? "",
				origin: "declared",
				defaultValue: e.value,
			})),
		[envs],
	)
	const [rows, setRows] = useState<EnvRow[]>(initialRows)

	useEffect(() => {
		setRows(initialRows)
	}, [initialRows])

	const create = useMutation(createAgent, {
		onMutate: () => ({
			toastId: toast.loading(`Creating ${name}…`),
		}),
		onSuccess: (_data, _vars, ctx) => {
			queryClient.invalidateQueries({
				queryKey: ["openotters.daemon.v1.Runtime", "ListAgents"],
			})
			toast.success(`Created ${name}`, { id: ctx?.toastId })
			setOpen(false)
			router.push(`/agents/${encodeURIComponent(name)}`)
		},
		onError: (err, _vars, ctx) => {
			toast.error("Create failed", {
				description: err.message,
				id: ctx?.toastId,
			})
		},
	})

	const updateRow = (idx: number, patch: Partial<EnvRow>) => {
		setRows((s) => s.map((r, i) => (i === idx ? { ...r, ...patch } : r)))
	}

	const removeRow = (idx: number) => {
		setRows((s) => s.filter((_, i) => i !== idx))
	}

	const addRow = () => {
		setRows((s) => [
			...s,
			{ key: "", value: "", description: "", origin: "custom", defaultValue: "" },
		])
	}

	const submit = () => {
		// Build env overrides:
		// - declared rows whose value DIFFERS from the image default
		//   are sent (overriding the bake-in).
		// - custom rows (user-added keys) are sent verbatim — every
		//   key is "new" by definition.
		// - declared rows still at the image default are skipped
		//   (no-op overrides waste metadata bytes).
		const envOverrides = rows
			.filter((r) => r.key !== "")
			.filter((r) => r.origin === "custom" || r.value !== r.defaultValue)
			.map((r) => ({
				key: r.key,
				value: r.value,
				description: r.description,
			}))

		create.mutate({
			name,
			ref: imageRef,
			model: model || "",
			envs: envOverrides,
		})
	}

	return (
		<Dialog onOpenChange={setOpen} open={open}>
			<DialogTrigger asChild>{trigger}</DialogTrigger>
			<DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
				<DialogHeader>
					<DialogTitle>Run agent</DialogTitle>
					<DialogDescription>
						Create a fresh agent from{" "}
						<code className="break-all font-mono text-xs">{imageRef}</code>. Override
						the model or any environment variable — leave a field unchanged to take the
						image's declared default.
					</DialogDescription>
				</DialogHeader>

				<div className="space-y-5 py-2">
					<div className="space-y-1.5">
						<Label htmlFor="agent-name">Agent name</Label>
						<Input
							id="agent-name"
							onChange={(e) => setName(e.target.value)}
							placeholder="hass-prod"
							value={name}
						/>
						<p className="text-muted-foreground text-xs">
							Must be unique within the daemon. Used as the URL slug.
						</p>
					</div>

					<div className="space-y-1.5">
						<Label htmlFor="model-override">Model override</Label>
						<Select onValueChange={setModel} value={model}>
							<SelectTrigger id="model-override">
								<SelectValue
									placeholder={
										models.isLoading
											? "Loading…"
											: (models.data?.models?.length ?? 0) === 0
												? "No models configured"
												: "use image's declared model"
									}
								/>
							</SelectTrigger>
							<SelectContent>
								{(models.data?.models ?? []).map((m) => (
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
						<p className="text-muted-foreground text-xs">
							Leave blank to use whatever <code className="font-mono">MODEL</code> the
							Agentfile declared.
						</p>
					</div>

					<div className="space-y-3">
						<div className="flex items-center justify-between gap-2">
							<div className="space-y-1">
								<Label>Environment overrides</Label>
								<p className="text-muted-foreground text-xs">
									{rows.some((r) => r.origin === "declared")
										? "Pre-filled with the image's declared defaults. Only changed rows are sent."
										: "Add per-run env vars merged with the image's declared defaults."}
								</p>
							</div>
							<Button onClick={addRow} size="sm" type="button" variant="outline">
								<Plus className="mr-1.5 h-3.5 w-3.5" />
								Add
							</Button>
						</div>
						{rows.length === 0 ? (
							<p className="rounded border border-dashed px-3 py-4 text-muted-foreground text-sm">
								No envs declared by the image. Click <strong>Add</strong> to set
								your own.
							</p>
						) : (
							<div className="space-y-3">
								{rows.map((row, idx) => {
									const isMultiline =
										row.value.length > 60 || row.value.includes("\n")
									const isChanged =
										row.origin === "custom" || row.value !== row.defaultValue
									return (
										<div
											className="space-y-1.5 rounded-lg border bg-muted/20 p-3"
											key={`${row.origin}-${idx}`}>
											<div className="flex items-baseline justify-between gap-2">
												{row.origin === "declared" ? (
													<Label
														className="break-all font-mono text-xs"
														htmlFor={`env-${idx}-value`}>
														{row.key}
													</Label>
												) : (
													<Input
														aria-label="env key"
														className="h-7 max-w-[220px] font-mono text-xs"
														onChange={(e) =>
															updateRow(idx, { key: e.target.value })
														}
														placeholder="VAR_NAME"
														value={row.key}
													/>
												)}
												<div className="flex items-center gap-2">
													{row.origin === "declared" && isChanged && (
														<button
															className="text-muted-foreground text-xs hover:text-foreground hover:underline"
															onClick={() =>
																updateRow(idx, { value: row.defaultValue })
															}
															type="button">
															reset
														</button>
													)}
													{row.origin === "custom" && (
														<button
															aria-label="remove env"
															className="text-muted-foreground hover:text-destructive"
															onClick={() => removeRow(idx)}
															type="button">
															<Trash2 className="h-3.5 w-3.5" />
														</button>
													)}
												</div>
											</div>
											{row.description && (
												<p className="text-muted-foreground text-xs">
													{row.description}
												</p>
											)}
											{isMultiline ? (
												<Textarea
													className="font-mono text-xs"
													id={`env-${idx}-value`}
													onChange={(e) =>
														updateRow(idx, { value: e.target.value })
													}
													rows={3}
													value={row.value}
												/>
											) : (
												<Input
													className="font-mono text-xs"
													id={`env-${idx}-value`}
													onChange={(e) =>
														updateRow(idx, { value: e.target.value })
													}
													placeholder={
														row.defaultValue === ""
															? "(leave empty)"
															: row.defaultValue
													}
													value={row.value}
												/>
											)}
										</div>
									)
								})}
							</div>
						)}
					</div>
				</div>

				<DialogFooter>
					<Button
						disabled={create.isPending}
						onClick={() => setOpen(false)}
						type="button"
						variant="outline">
						Cancel
					</Button>
					<Button
						disabled={create.isPending || name === ""}
						onClick={submit}
						type="button">
						<Play className="mr-2 h-4 w-4" />
						{create.isPending ? "Creating…" : "Run"}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	)
}
