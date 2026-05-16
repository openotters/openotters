"use client"

import { useMutation, useQuery } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { Pin, PinOff, Plus, Trash2 } from "lucide-react"
import { useState } from "react"
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
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Textarea } from "@/components/ui/textarea"
import type { AgentNote } from "@/lib/proto/v1/daemon_pb"
import {
	deleteAgentNote,
	getAgentNote,
	listAgentNotes,
	saveAgentNote,
	setAgentNoteInContext,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"

interface NotesPanelProps {
	ref: string
}

// NotesPanel is the per-agent notes UI under the "Notes" tab on
// /agents/[agent]. Renders a list view + a selected-note detail
// pane. The model writes notes via the note_* tools; this panel is
// the operator's read/edit surface.
//
// The split-pane layout mirrors a typical inbox UI — a compact list
// on the left (cached preview + pin indicator) and the full body
// on the right via a separate GetAgentNote call so the list
// payload stays bounded.
export function NotesPanel({ ref }: NotesPanelProps) {
	const queryClient = useQueryClient()
	const list = useQuery(listAgentNotes, { ref, onlyInContext: false })

	const [selectedKey, setSelectedKey] = useState<string | null>(null)
	const [editorOpen, setEditorOpen] = useState(false)
	const [editorKey, setEditorKey] = useState("")
	const [editorContent, setEditorContent] = useState("")
	const [editorMode, setEditorMode] = useState<"create" | "edit">("create")

	// On mutation success we invalidate both the list (so the
	// preview / pin badge updates) and the selected note's GetNote
	// query (so the right pane re-fetches the full body). The
	// daemon doesn't push notifications, so client-driven
	// invalidation is the right tool.
	const invalidate = () => {
		queryClient.invalidateQueries({ predicate: (q) => {
			const k = q.queryKey
			return Array.isArray(k) && typeof k[0] === "object"
		}})
	}

	const saveMut = useMutation(saveAgentNote, {
		onSuccess: (resp) => {
			toast.success(resp.overwrote ? `Overwrote "${resp.note?.key}"` : `Saved "${resp.note?.key}"`)
			setEditorOpen(false)
			setSelectedKey(resp.note?.key ?? null)
			invalidate()
		},
		onError: (err) => toast.error(err.message),
	})

	const deleteMut = useMutation(deleteAgentNote, {
		onSuccess: (resp, vars) => {
			toast.success(resp.deleted ? `Deleted "${vars.key}"` : `"${vars.key}" already absent`)
			if (selectedKey === vars.key) setSelectedKey(null)
			invalidate()
		},
		onError: (err) => toast.error(err.message),
	})

	const pinMut = useMutation(setAgentNoteInContext, {
		onSuccess: (resp) => {
			toast.success(resp.note?.inContext ? `Pinned "${resp.note?.key}"` : `Unpinned "${resp.note?.key}"`)
			invalidate()
		},
		onError: (err) => toast.error(err.message),
	})

	const openCreate = () => {
		setEditorMode("create")
		setEditorKey("")
		setEditorContent("")
		setEditorOpen(true)
	}

	const openEdit = (note: AgentNote) => {
		setEditorMode("edit")
		setEditorKey(note.key)
		setEditorContent(note.content)
		setEditorOpen(true)
	}

	const submitEdit = () => {
		if (!editorKey.trim()) {
			toast.error("Key is required")
			return
		}
		saveMut.mutate({ ref, key: editorKey.trim(), content: editorContent })
	}

	const notes = list.data?.notes ?? []
	const selected = notes.find((n) => n.key === selectedKey)

	return (
		<Card>
			<CardHeader>
				<div className="flex items-center justify-between">
					<div>
						<CardTitle>Notes</CardTitle>
						<CardDescription>
							Durable per-agent facts. Saved here persist across sessions. Pin a note to inject its full
							content into the system prompt on every step.
						</CardDescription>
					</div>
					<Button onClick={openCreate} size="sm">
						<Plus className="mr-1 h-4 w-4" />
						New note
					</Button>
				</div>
			</CardHeader>
			<CardContent>
				{list.isLoading ? (
					<p className="py-6 text-center text-muted-foreground">Loading…</p>
				) : list.error ? (
					<p className="py-6 text-center text-destructive">{list.error.message}</p>
				) : notes.length === 0 ? (
					<p className="py-6 text-center text-muted-foreground">
						No notes stored. The agent saves notes via its note_save tool when it learns
						something worth remembering — or click "New note" to add one yourself.
					</p>
				) : (
					<div className="grid gap-4 md:grid-cols-[260px_1fr]">
						<ScrollArea className="h-[60vh] rounded-md border">
							<div className="divide-y">
								{notes.map((n) => (
									<button
										className={`w-full px-3 py-2 text-left text-sm transition hover:bg-muted ${
											n.key === selectedKey ? "bg-muted" : ""
										}`}
										key={n.key}
										onClick={() => setSelectedKey(n.key)}
										type="button"
									>
										<div className="flex items-center gap-2">
											<code className="truncate font-mono text-xs">{n.key}</code>
											{n.inContext && (
												<Badge className="h-5 px-1 py-0 text-[10px]" variant="default">
													pinned
												</Badge>
											)}
										</div>
										<p className="mt-1 truncate text-xs text-muted-foreground">{n.preview}</p>
									</button>
								))}
							</div>
						</ScrollArea>

						<div className="rounded-md border">
							{selected ? (
								<NoteDetail
									agentRef={ref}
									note={selected}
									onDelete={(key) => deleteMut.mutate({ ref, key })}
									onEdit={openEdit}
									onTogglePin={(note) =>
										pinMut.mutate({ ref, key: note.key, inContext: !note.inContext })
									}
								/>
							) : (
								<p className="py-6 text-center text-muted-foreground">
									Select a note to view its full content.
								</p>
							)}
						</div>
					</div>
				)}
			</CardContent>

			<Dialog onOpenChange={setEditorOpen} open={editorOpen}>
				<DialogContent className="sm:max-w-2xl">
					<DialogHeader>
						<DialogTitle>{editorMode === "create" ? "New note" : `Edit ${editorKey}`}</DialogTitle>
						<DialogDescription>
							Re-using an existing key overwrites the prior note. Keys must be lowercase, ≤ 64
							chars, [a-z0-9_-].
						</DialogDescription>
					</DialogHeader>
					<div className="space-y-3">
						<div>
							<Label htmlFor="note-key">Key</Label>
							<Input
								disabled={editorMode === "edit"}
								id="note-key"
								onChange={(e) => setEditorKey(e.target.value)}
								placeholder="e.g. k8s-cluster"
								value={editorKey}
							/>
						</div>
						<div>
							<Label htmlFor="note-content">Content</Label>
							<Textarea
								className="h-48 font-mono text-sm"
								id="note-content"
								onChange={(e) => setEditorContent(e.target.value)}
								placeholder="Free-form text. Markdown OK."
								value={editorContent}
							/>
						</div>
					</div>
					<DialogFooter>
						<Button onClick={() => setEditorOpen(false)} variant="outline">
							Cancel
						</Button>
						<Button disabled={saveMut.isPending} onClick={submitEdit}>
							{saveMut.isPending ? "Saving…" : "Save"}
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>
		</Card>
	)
}

interface NoteDetailProps {
	agentRef: string
	note: AgentNote
	onTogglePin: (note: AgentNote) => void
	onEdit: (note: AgentNote) => void
	onDelete: (key: string) => void
}

// NoteDetail re-fetches the full body via GetAgentNote so the list
// response stays bounded. The preview the list returned is fine for
// the title row; the body is what the operator actually reads.
function NoteDetail({ agentRef, note, onTogglePin, onEdit, onDelete }: NoteDetailProps) {
	const detail = useQuery(getAgentNote, { ref: agentRef, key: note.key })
	const body = detail.data?.note?.content ?? ""

	return (
		<div className="space-y-3 p-4">
			<div className="flex items-start justify-between gap-2">
				<div className="min-w-0">
					<h3 className="break-all font-mono text-base">{note.key}</h3>
					<p className="text-xs text-muted-foreground">
						{note.inContext ? "Pinned — flows into system prompt" : "Not pinned"} ·{" "}
						{formatRelative(note.updatedAt)}
					</p>
				</div>
				<div className="flex gap-1">
					<Button onClick={() => onTogglePin(note)} size="sm" variant="outline">
						{note.inContext ? (
							<>
								<PinOff className="mr-1 h-3 w-3" />
								Unpin
							</>
						) : (
							<>
								<Pin className="mr-1 h-3 w-3" />
								Pin
							</>
						)}
					</Button>
					<Button onClick={() => onEdit({ ...note, content: body })} size="sm" variant="outline">
						Edit
					</Button>
					<ConfirmDelete
						confirmLabel="Delete note"
						description={`This will permanently delete "${note.key}".`}
						onConfirm={() => onDelete(note.key)}
						title="Delete this note?"
						trigger={(open) => (
							<Button onClick={open} size="sm" variant="ghost">
								<Trash2 className="h-3 w-3" />
							</Button>
						)}
					/>
				</div>
			</div>

			{detail.isLoading ? (
				<p className="text-sm text-muted-foreground">Loading…</p>
			) : detail.error ? (
				<p className="text-sm text-destructive">{detail.error.message}</p>
			) : (
				<ScrollArea className="h-[44vh]">
					<pre className="whitespace-pre-wrap break-words font-mono text-sm">{body}</pre>
				</ScrollArea>
			)}
		</div>
	)
}

// formatRelative renders a coarse "how long ago" string. Same
// resolution the runtime uses in the system-prompt preview table
// — durable facts don't need second-granularity.
function formatRelative(unixSec: bigint): string {
	const ms = Number(unixSec) * 1000
	if (!ms) return "—"
	const d = (Date.now() - ms) / 1000
	if (d < 60) return "just now"
	if (d < 3600) return `${Math.floor(d / 60)}m ago`
	if (d < 86400) return `${Math.floor(d / 3600)}h ago`
	return `${Math.floor(d / 86400)}d ago`
}
