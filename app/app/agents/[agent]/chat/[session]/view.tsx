"use client"

import { createClient, type Transport } from "@connectrpc/connect"
import { useQuery, useTransport } from "@connectrpc/connect-query"
import {
	ArrowLeft,
	Bot,
	Check,
	ChevronDown,
	ChevronRight,
	Circle,
	Copy,
	Loader2,
	Pencil,
	RefreshCw,
	Square,
	Terminal as TerminalIcon,
	X as XIcon,
} from "lucide-react"
import Link from "next/link"
import { useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"
import {
	Conversation,
	ConversationContent,
	ConversationEmptyState,
	ConversationScrollButton,
} from "@/components/ai-elements/conversation"
import {
	Message,
	MessageAction,
	MessageActions,
	MessageContent,
	MessageResponse,
} from "@/components/ai-elements/message"
import {
	PromptInput,
	PromptInputBody,
	PromptInputFooter,
	PromptInputSubmit,
	PromptInputTextarea,
	PromptInputTools,
	type PromptInputMessage,
} from "@/components/ai-elements/prompt-input"
import { Shimmer } from "@/components/ai-elements/shimmer"
import { ToolInput, ToolOutput } from "@/components/ai-elements/tool"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { listAgents, listSessionMessages } from "@/lib/proto/v1/daemon-Runtime_connectquery"
import { Runtime } from "@/lib/proto/v1/daemon_pb"
import { useRouteParams } from "@/lib/use-route-params"

// Generic fallback suggestions for agents that report no BIN tools.
// When agent.tools is non-empty the empty state lists those instead
// (see emptyStateSuggestions).
const FALLBACK_SUGGESTIONS: string[] = [
	"What can you do?",
	"Walk me through your tools.",
	"Show me an example task you handle well.",
]

// emptyStateSuggestions builds quick-prompt text from the agent's
// declared BIN tools. The empty state is the model's first
// impression — surfacing the actual capability list is far more
// useful than generic prose.
function emptyStateSuggestions(toolNames: string[]): string[] {
	if (toolNames.length === 0) return FALLBACK_SUGGESTIONS
	const sample = toolNames.slice(0, 4)
	return [
		`What can you do? You have ${toolNames.length} tools available.`,
		`Use ${sample[0]} to show me something interesting.`,
		"Walk me through your tools and what each one does.",
	]
}

// Rough char-based token estimate. ~4 chars / token holds well enough
// for English; we don't bundle a real tokenizer in the dashboard.
function estimateTokens(s: string): number {
	return Math.ceil(s.length / 4)
}

// Per-message rendering decomposes the assistant's reply into ordered
// parts. Text deltas accumulate into the trailing "text" part; each
// tool.call / tool.result pair gets its own collapsible Tool block,
// rendered inline between text segments so the timeline reads in the
// order the model produced it.
type Part =
	| { kind: "text"; content: string }
	| {
			kind: "tool"
			id: string
			name: string
			input: unknown
			output: string | null
			state: "input-available" | "output-available"
	  }

interface UIMessage {
	id: string
	role: "user" | "assistant"
	// Branches let the user regenerate without losing the prior
	// answer: each call to "Regenerate" appends a new array of parts
	// and bumps activeBranch to it; the message UI exposes prev/next
	// to flip between them. branches[activeBranch] is what renders.
	branches: Part[][]
	activeBranch: number
	createdAt: number // unix seconds
	failed?: boolean // assistant turn that errored — surfaces a Retry CTA
}

// formatRelative returns a coarse "Xh ago" string suitable for a
// turn timestamp. Falls back to the locale date string for older
// messages where minutes-since-now stops being useful.
function formatRelative(unixSec: number): string {
	const diff = Math.max(0, Math.floor(Date.now() / 1000) - unixSec)
	if (diff < 60) return "just now"
	if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
	if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
	if (diff < 7 * 86400) return `${Math.floor(diff / 86400)}d ago`
	return new Date(unixSec * 1000).toLocaleDateString("en-US", {
		month: "short",
		day: "numeric",
	})
}

// toolStatusColor classifies a tool part's outcome based on its
// state + output payload. Returns a tuple of (icon, classes) used by
// CompactToolRow. We can't always trust output-available to mean
// success (the model may stream an error string back as output), so
// we heuristically scan for "error" / "failed" / "exit code" and
// downgrade to red.
type ToolStatus = "running" | "ok" | "error"

function classifyToolStatus(p: Extract<Part, { kind: "tool" }>): ToolStatus {
	if (p.state === "input-available") return "running"
	const out = (p.output ?? "").toLowerCase()
	if (
		out.includes("\nerror") ||
		out.startsWith("error") ||
		out.includes("traceback") ||
		out.includes("exit code") ||
		out.includes("permission denied") ||
		out.includes("no such file")
	) {
		return "error"
	}
	return "ok"
}

// summarizeToolInput produces a short one-line preview of a tool
// call's arguments — first string value the model passed, falling
// back to a JSON snippet. Used by the compact tool row.
function summarizeToolInput(input: unknown): string {
	if (typeof input === "string") return input.length > 60 ? `${input.slice(0, 60)}…` : input
	if (input === null || input === undefined) return ""
	if (typeof input === "object") {
		for (const v of Object.values(input as Record<string, unknown>)) {
			if (typeof v === "string" && v.length > 0) {
				return v.length > 60 ? `${v.slice(0, 60)}…` : v
			}
		}
		try {
			const j = JSON.stringify(input)
			return j.length > 60 ? `${j.slice(0, 60)}…` : j
		} catch {
			return ""
		}
	}
	return String(input)
}

// groupParts collapses runs of consecutive tool parts into a single
// "step" group. A long agent run that calls 12 tools in a row renders
// as one card with "(12 actions)" instead of 12 stacked Tool blocks
// pushing the conversation off-screen.
type RenderGroup =
	| { kind: "text"; key: string; content: string }
	| { kind: "tools"; key: string; items: Extract<Part, { kind: "tool" }>[] }

function groupParts(parts: Part[], baseKey: string): RenderGroup[] {
	const out: RenderGroup[] = []
	for (let i = 0; i < parts.length; i++) {
		const p = parts[i]
		if (p.kind === "text") {
			out.push({ kind: "text", key: `${baseKey}-t-${i}`, content: p.content })
			continue
		}
		const last = out[out.length - 1]
		if (last && last.kind === "tools") {
			last.items.push(p)
			continue
		}
		out.push({ kind: "tools", key: `${baseKey}-g-${i}`, items: [p] })
	}
	return out
}

function tryParseJson(s: string): unknown {
	try {
		return JSON.parse(s)
	} catch {
		return s
	}
}

// pushTextDelta appends a text chunk to the message's parts. A new
// "text" part is created whenever the previous trailing part was a
// tool, so a model that interleaves text + tools renders in order.
function pushTextDelta(parts: Part[], chunk: string): Part[] {
	if (parts.length === 0 || parts[parts.length - 1].kind !== "text") {
		return [...parts, { kind: "text", content: chunk }]
	}
	const next = parts.slice(0, -1)
	const last = parts[parts.length - 1]
	if (last.kind === "text") {
		next.push({ kind: "text", content: last.content + chunk })
	}
	return next
}

function pushToolCall(parts: Part[], id: string, name: string, content: string): Part[] {
	return [
		...parts,
		{
			kind: "tool",
			id,
			name,
			input: tryParseJson(content),
			output: null,
			state: "input-available",
		},
	]
}

// attachToolResult flips the most recent still-running tool call with
// the matching name to "output-available" and records the result.
// We match on name because the daemon's ChatStreamEvent doesn't
// carry the tool-call ID — it only has the tool name + content.
function attachToolResult(parts: Part[], name: string, content: string): Part[] {
	const next = parts.slice()
	for (let i = next.length - 1; i >= 0; i--) {
		const p = next[i]
		if (p.kind === "tool" && p.name === name && p.state === "input-available") {
			next[i] = { ...p, output: content, state: "output-available" }
			return next
		}
	}
	// No matching call — surface the result as its own block so it
	// isn't silently swallowed.
	return [
		...next,
		{
			kind: "tool",
			id: `${name}-${next.length}`,
			name,
			input: undefined,
			output: content,
			state: "output-available",
		},
	]
}

export default function ChatPage() {
	const params = useRouteParams<{ agent: string; session: string }>(
		"/agents/:agent/chat/:session",
	)
	const agentName = params.agent ?? ""
	// The session segment carries the conversation across reloads.
	// /chat (no segment) redirects here with a freshly-minted id; we
	// just trust whatever's in the URL.
	const sessionId = params.session ?? ""
	const transport = useTransport() as Transport
	const client = useMemo(() => createClient(Runtime, transport), [transport])

	const agents = useQuery(listAgents, {}, { enabled: agentName !== "" })
	const agent = agents.data?.agents.find((a) => a.name === agentName)

	const [messages, setMessages] = useState<UIMessage[]>([])
	const [input, setInput] = useState("")
	const [status, setStatus] = useState<"ready" | "submitted" | "streaming" | "error">("ready")
	const [error, setError] = useState<string | null>(null)
	const [copiedId, setCopiedId] = useState<string | null>(null)
	// Tracks which assistant message just had a branch switch so
	// the UI can flash it (see flashBranch helper); cleared after
	// the animation window. Keyed by message id.
	const [flashedId, setFlashedId] = useState<string | null>(null)
	// Toggle that collapses every tool block at once; useful when
	// re-reading a long transcript. Drives a `forceClosed` prop
	// down through the tool-card render.
	const [densityCompact, setDensityCompact] = useState(false)
	const composerRef = useRef<HTMLTextAreaElement | null>(null)
	const abortRef = useRef<AbortController | null>(null)
	// Re-render every minute so relative timestamps tick. Cheap; the
	// state value itself isn't used.
	const [, setNow] = useState(Date.now())
	useEffect(() => {
		const i = setInterval(() => setNow(Date.now()), 60_000)
		return () => clearInterval(i)
	}, [])

	// Keyboard shortcuts:
	//   Cmd/Ctrl-K  → focus the composer
	//   Esc         → stop a streaming response
	//   ↑ (empty)   → load the most recent user message into the
	//                  composer for editing (only when input is empty
	//                  and the composer is focused)
	useEffect(() => {
		const handler = (e: KeyboardEvent) => {
			if ((e.metaKey || e.ctrlKey) && e.key === "k") {
				e.preventDefault()
				composerRef.current?.focus()
				return
			}
			if (e.key === "Escape" && (status === "submitted" || status === "streaming")) {
				e.preventDefault()
				abortRef.current?.abort()
				return
			}
			if (
				e.key === "ArrowUp" &&
				input === "" &&
				document.activeElement === composerRef.current
			) {
				const last = [...messages].reverse().find((m) => m.role === "user")
				if (!last) return
				e.preventDefault()
				setInput(extractText(last))
			}
		}
		window.addEventListener("keydown", handler)
		return () => window.removeEventListener("keydown", handler)
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [status, input, messages])

	// Hydrate the message log from the daemon's session store on mount
	// (and after the URL params resolve client-side via useRouteParams).
	// The daemon persists user / assistant text turns per session id;
	// tool-call deltas aren't in this log (they're stream-only), so a
	// rehydrated transcript shows clean alternating turns.
	const history = useQuery(
		listSessionMessages,
		{ ref: agentName, sessionId, limit: 0 },
		{ enabled: agentName !== "" && sessionId !== "" },
	)

	const hydratedRef = useRef<string>("")
	useEffect(() => {
		const key = `${agentName}|${sessionId}`
		if (hydratedRef.current === key) return
		if (!history.data) return
		hydratedRef.current = key
		const persisted: UIMessage[] = history.data.messages
			.filter((m) => m.role === "user" || m.role === "assistant")
			.map((m, i) => ({
				id: `hist-${i}-${m.role}`,
				role: m.role as "user" | "assistant",
				branches: [[{ kind: "text", content: m.content }]],
				activeBranch: 0,
				createdAt: Number(m.createdAt),
			}))
		if (persisted.length > 0) {
			setMessages(persisted)
		}
	}, [agentName, sessionId, history.data])

	// extractText flattens a message's active branch into a single
	// string. By default it returns text only; pass includeTools=true
	// to fold tool inputs/outputs in too — useful for "copy whole
	// turn" debugging where the user wants the full execution trace.
	const extractText = (message: UIMessage, includeTools = false): string => {
		const parts = message.branches[message.activeBranch]
		const lines: string[] = []
		for (const p of parts) {
			if (p.kind === "text") {
				lines.push(p.content)
				continue
			}
			if (!includeTools) continue
			const inputJSON =
				p.input === undefined ? "" : JSON.stringify(p.input, null, 2)
			lines.push(
				[
					`> ${p.name}(${inputJSON || ""})`,
					p.output ? `< ${p.output}` : "",
				]
					.filter(Boolean)
					.join("\n"),
			)
		}
		return lines.join("\n")
	}

	// updateActiveBranch returns a new UIMessage with `fn` applied to
	// its currently-active branch. Keeps the immutable-state pattern
	// the streaming reducer depends on.
	const updateActiveBranch = (m: UIMessage, fn: (parts: Part[]) => Part[]): UIMessage => {
		const next = m.branches.slice()
		next[m.activeBranch] = fn(m.branches[m.activeBranch])
		return { ...m, branches: next }
	}

	// sendPrompt mode:
	//   "new"        — append a fresh user + assistant turn.
	//   "regenerate" — leave the trailing user message in place, add
	//                  a *new branch* on the assistant message at
	//                  targetAssistantId so the prior answer stays
	//                  reachable via the branch selector.
	const sendPrompt = async (
		prompt: string,
		opts?: { mode?: "new" | "regenerate"; targetAssistantId?: string },
	) => {
		if (!prompt || agentName === "" || status === "streaming") {
			return
		}

		const mode = opts?.mode ?? "new"
		const nowSec = Math.floor(Date.now() / 1000)
		let assistantId: string

		if (mode === "regenerate" && opts?.targetAssistantId) {
			assistantId = opts.targetAssistantId
			setMessages((prev) =>
				prev.map((m) =>
					m.id === assistantId
						? {
								...m,
								branches: [...m.branches, []],
								activeBranch: m.branches.length,
								createdAt: nowSec,
								failed: false,
							}
						: m,
				),
			)
		} else {
			const userMessage: UIMessage = {
				id: `msg-${Date.now()}-user`,
				role: "user",
				branches: [[{ kind: "text", content: prompt }]],
				activeBranch: 0,
				createdAt: nowSec,
			}
			assistantId = `msg-${Date.now()}-assistant`
			const assistantSeed: UIMessage = {
				id: assistantId,
				role: "assistant",
				branches: [[]],
				activeBranch: 0,
				createdAt: nowSec,
			}
			setMessages((prev) => [...prev, userMessage, assistantSeed])
			setInput("")
		}

		setStatus("submitted")
		setError(null)

		const ac = new AbortController()
		abortRef.current = ac

		try {
			const stream = client.chatStreamWithAgent(
				{ ref: agentName, sessionId, prompt },
				{ signal: ac.signal },
			)

			let toolCounter = 0
			setStatus("streaming")

			for await (const event of stream) {
				setMessages((prev) =>
					prev.map((m) => {
						if (m.id !== assistantId) return m
						switch (event.type) {
							case "text.delta":
								return updateActiveBranch(m, (parts) => pushTextDelta(parts, event.content))
							case "tool.call":
								toolCounter += 1
								return updateActiveBranch(m, (parts) =>
									pushToolCall(parts, `${event.tool}-${toolCounter}`, event.tool, event.content),
								)
							case "tool.result":
								return updateActiveBranch(m, (parts) =>
									attachToolResult(parts, event.tool, event.content),
								)
							// step.start / step.finish are intentionally dropped —
							// they're per-step bookkeeping that doesn't add visual
							// information once text + tool blocks render in order.
							default:
								return m
						}
					}),
				)
			}

			setStatus("ready")
		} catch (err) {
			const aborted = err instanceof Error && err.name === "AbortError"
			if (!aborted) {
				setError(err instanceof Error ? err.message : String(err))
				setMessages((prev) =>
					prev.map((m) => (m.id === assistantId ? { ...m, failed: true } : m)),
				)
				setStatus("error")
			} else {
				setStatus("ready")
			}
		} finally {
			abortRef.current = null
		}
	}

	// stopStreaming aborts whatever stream is in flight. The catch
	// block in sendPrompt detects the AbortError and resets status to
	// "ready" without surfacing it as an error.
	const stopStreaming = () => {
		abortRef.current?.abort()
	}

	const handleSubmit = (msg: PromptInputMessage) => sendPrompt(msg.text.trim())

	// Regenerate adds a new branch to the assistant turn at
	// assistantId. The prior answer stays reachable through the
	// branch-selector arrows below the message.
	const handleRegenerate = (assistantId: string) => {
		const idx = messages.findIndex((m) => m.id === assistantId)
		if (idx <= 0) return
		const userMsg = messages[idx - 1]
		if (userMsg.role !== "user") return
		const prompt = extractText(userMsg)
		if (!prompt) return
		void sendPrompt(prompt, { mode: "regenerate", targetAssistantId: assistantId })
	}

	// handleRetry re-sends a user message verbatim. Drops the user
	// message and any subsequent turns; sendPrompt re-creates them.
	const handleRetry = (userId: string) => {
		const idx = messages.findIndex((m) => m.id === userId)
		if (idx < 0) return
		const userMsg = messages[idx]
		if (userMsg.role !== "user") return
		const prompt = extractText(userMsg)
		if (!prompt) return
		setMessages((prev) => prev.slice(0, idx))
		void sendPrompt(prompt)
	}

	// handleRetryAfterError targets the failed assistant message
	// directly: re-sends the preceding user prompt and reuses the
	// same assistant turn (replaces the failed branch).
	const handleRetryAfterError = (assistantId: string) => {
		const idx = messages.findIndex((m) => m.id === assistantId)
		if (idx <= 0) return
		const userMsg = messages[idx - 1]
		if (userMsg.role !== "user") return
		const prompt = extractText(userMsg)
		if (!prompt) return
		// Replace the failed branch in place rather than appending,
		// so retry → success doesn't leave a "<error> | <success>"
		// branch selector for the user to navigate.
		setMessages((prev) =>
			prev.map((m) =>
				m.id === assistantId
					? {
							...m,
							branches: m.branches.map((b, i) => (i === m.activeBranch ? [] : b)),
							failed: false,
						}
					: m,
			),
		)
		void sendPrompt(prompt, { mode: "regenerate", targetAssistantId: assistantId })
	}

	// handleEdit puts the user message back in the prompt input and
	// truncates the conversation up to (but not including) that
	// message — submitting then resends from that point with the new
	// text.
	const handleEdit = (userId: string) => {
		const idx = messages.findIndex((m) => m.id === userId)
		if (idx < 0) return
		const userMsg = messages[idx]
		if (userMsg.role !== "user") return
		setInput(extractText(userMsg))
		setMessages((prev) => prev.slice(0, idx))
	}

	const switchBranch = (id: string, dir: -1 | 1) => {
		setMessages((prev) =>
			prev.map((m) => {
				if (m.id !== id) return m
				const total = m.branches.length
				if (total <= 1) return m
				const next = (m.activeBranch + dir + total) % total
				return { ...m, activeBranch: next }
			}),
		)
		// Flash the message + scroll it into view so the user can
		// see what changed when they flip branches.
		setFlashedId(id)
		setTimeout(() => {
			const el = document.querySelector(`[data-message-id="${id}"]`)
			el?.scrollIntoView({ behavior: "smooth", block: "center" })
		}, 0)
		setTimeout(() => setFlashedId((cur) => (cur === id ? null : cur)), 900)
	}

	const handleCopy = async (message: UIMessage, includeTools = false) => {
		const text = extractText(message, includeTools)
		if (!text) {
			toast.error("Nothing to copy in this message")
			return
		}
		try {
			await navigator.clipboard.writeText(text)
			setCopiedId(message.id)
			setTimeout(() => setCopiedId((cur) => (cur === message.id ? null : cur)), 1500)
		} catch (err) {
			toast.error("Copy failed", {
				description: err instanceof Error ? err.message : String(err),
			})
		}
	}

	return (
		// Chat fills the main pane via h-full and lets the internal
		// <Conversation> scroll on its own. min-h-0 lets the flex
		// child shrink so descendant overflow:auto actually engages.
		// overflow-hidden caps long messages from breaking the layout
		// — pre/code blocks scroll inside the message.
		<div className="flex h-full min-h-0 w-full max-w-full flex-col overflow-hidden">
			<div className="flex shrink-0 items-center gap-4 border-b pb-4">
				<Button asChild size="icon" variant="ghost">
					<Link href={`/agents/${agentName}`}>
						<ArrowLeft className="h-4 w-4" />
					</Link>
				</Button>
				<div className="flex min-w-0 flex-1 items-center gap-3">
					<div className="relative flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10">
						<Bot className="h-5 w-5 text-primary" />
						{agent && (
							<span
								aria-label={`Agent ${agent.status}`}
								className={`absolute right-0.5 bottom-0.5 inline-flex h-2.5 w-2.5 ${
									agent.status === "running"
										? "rounded-full bg-emerald-500 ring-2 ring-background animate-pulse"
										: "rounded-full bg-muted-foreground/40 ring-2 ring-background"
								}`}
							/>
						)}
					</div>
					<div className="min-w-0 flex-1">
						<h1 className="truncate font-semibold">{agentName || "—"}</h1>
						<p className="truncate font-mono text-muted-foreground text-xs">
							{agent?.model ? `${agent.model} · ` : ""}session {sessionId.slice(-8)}
						</p>
					</div>
				</div>
				<Button
					aria-label={densityCompact ? "Expand all tool blocks" : "Collapse all tool blocks"}
					onClick={() => setDensityCompact((v) => !v)}
					size="sm"
					title={densityCompact ? "Expand all tool blocks" : "Collapse all tool blocks"}
					variant="ghost">
					{densityCompact ? (
						<ChevronDown className="h-4 w-4" />
					) : (
						<ChevronRight className="h-4 w-4" />
					)}
				</Button>
			</div>

			<Conversation className="min-h-0 flex-1">
				<ConversationContent>
					{/* Loading skeleton: history-fetch in flight on a session
					    that has prior messages. Cheaper read than waiting
					    for the empty state to flash and then disappear. */}
					{messages.length === 0 && history.isLoading && (
						<div className="space-y-6 py-4">
							{[0, 1, 2].map((i) => (
								<div
									className={`flex max-w-[95%] flex-col gap-2 ${i % 2 === 0 ? "" : "ml-auto items-end"}`}
									key={i}>
									<div
										className={`h-3 w-2/3 animate-pulse rounded bg-muted ${
											i % 2 === 0 ? "" : "ml-auto"
										}`}
									/>
									<div
										className={`h-3 w-1/2 animate-pulse rounded bg-muted ${
											i % 2 === 0 ? "" : "ml-auto"
										}`}
									/>
								</div>
							))}
						</div>
					)}
					{messages.length === 0 && !history.isLoading && (
						<>
							<ConversationEmptyState
								description={
									agent && agent.tools && agent.tools.length > 0
										? `${agentName} has ${agent.tools.length} tool${agent.tools.length === 1 ? "" : "s"} available.`
										: "Send a message to start. Streaming text + tool calls render in order."
								}
								icon={<Bot className="h-8 w-8 text-muted-foreground/50" />}
								title={agentName ? `Talk to ${agentName}` : "No messages yet"}
							/>
							{agent && agent.tools && agent.tools.length > 0 && (
								<div className="flex flex-wrap justify-center gap-1.5 pt-1">
									{agent.tools.slice(0, 8).map((t) => (
										<span
											className="inline-flex items-center gap-1 rounded-md bg-secondary px-2 py-0.5 font-mono text-secondary-foreground text-xs"
											key={t.name}>
											<TerminalIcon className="h-3 w-3" />
											{t.name}
										</span>
									))}
									{agent.tools.length > 8 && (
										<span className="inline-flex items-center rounded-md bg-secondary px-2 py-0.5 text-secondary-foreground text-xs">
											+{agent.tools.length - 8} more
										</span>
									)}
								</div>
							)}
							<div className="flex flex-wrap justify-center gap-2 pt-2">
								{emptyStateSuggestions(
									(agent?.tools ?? []).map((t) => t.name),
								).map((s) => (
									<Button
										className="h-auto whitespace-normal rounded-full text-xs"
										disabled={agentName === "" || status === "streaming"}
										key={s}
										onClick={() => sendPrompt(s)}
										size="sm"
										variant="outline">
										{s}
									</Button>
								))}
							</div>
						</>
					)}
					{messages.map((message, msgIdx) => {
						const isLastAssistant =
							message.role === "assistant" && msgIdx === messages.length - 1
						const isStreamingThis =
							isLastAssistant && (status === "submitted" || status === "streaming")
						const activeParts = message.branches[message.activeBranch] ?? []
						const isEmptyAssistant = message.role === "assistant" && activeParts.length === 0
						const groups = groupParts(activeParts, `${message.id}-b${message.activeBranch}`)
						return (
							<div
								className={`relative ${
									flashedId === message.id
										? "rounded-lg ring-2 ring-primary/40 transition-shadow"
										: ""
								}`}
								data-message-id={message.id}
								key={message.id}>
								<Message from={message.role}>
								{/*
									`min-w-0` lets the flex column shrink past its
									intrinsic content width so long markdown lines
									and code-block contents wrap / scroll inside the
									message instead of pushing the page wider. The
									descendant selectors keep streamdown's <pre>
									blocks scrollable horizontally on overflow rather
									than hijacking the document scroll. User bubbles
									cap at 70% so a 30-char and a 300-char message
									don't render at wildly different widths.
								*/}
								<MessageContent className={`min-w-0 max-w-full overflow-hidden break-words [&_pre]:max-w-full [&_pre]:overflow-x-auto [&_table]:block [&_table]:max-w-full [&_table]:overflow-x-auto [&_code]:break-all [&_a]:break-all ${message.role === "user" ? "max-w-[70%]" : ""}`}>
									{isEmptyAssistant && isStreamingThis && (
										<Shimmer className="text-sm">Thinking…</Shimmer>
									)}
									{message.failed && isEmptyAssistant && !isStreamingThis && (
										<div className="flex items-center justify-between gap-3 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm">
											<span className="text-destructive">
												Stream failed — the model didn't produce a reply.
											</span>
											<Button
												onClick={() => handleRetryAfterError(message.id)}
												size="sm"
												variant="outline">
												<RefreshCw className="mr-2 h-3.5 w-3.5" />
												Retry
											</Button>
										</div>
									)}
									{groups.map((g, gIdx) => {
										if (g.kind === "text") {
											const lastTextIdx = groups.length - 1
											const animating = isStreamingThis && gIdx === lastTextIdx
											return (
												<div className="relative" key={g.key}>
													<MessageResponse isAnimating={animating}>
														{g.content}
													</MessageResponse>
													{animating && (
														<span
															aria-hidden="true"
															className="ml-0.5 inline-block h-3.5 w-1.5 animate-pulse bg-foreground align-text-bottom"
														/>
													)}
												</div>
											)
										}
										return (
											<div className="space-y-1" key={g.key}>
												{g.items.map((part) => (
													<CompactToolRow
														densityCompact={densityCompact}
														key={part.id}
														part={part}
													/>
												))}
											</div>
										)
									})}
								</MessageContent>
								{(() => {
									const showAssistantActions =
										message.role === "assistant" &&
										activeParts.length > 0 &&
										!isStreamingThis
									const showUserActions =
										message.role === "user" && status !== "streaming"
									const branchControls =
										message.role === "assistant" && message.branches.length > 1 ? (
											<div className="inline-flex items-center gap-1 rounded-md border bg-background px-1 text-muted-foreground text-xs">
												<button
													className="px-1 hover:text-foreground disabled:opacity-30"
													disabled={status === "streaming"}
													onClick={() => switchBranch(message.id, -1)}
													type="button">
													‹
												</button>
												<span className="font-mono">
													{message.activeBranch + 1}/{message.branches.length}
												</span>
												<button
													className="px-1 hover:text-foreground disabled:opacity-30"
													disabled={status === "streaming"}
													onClick={() => switchBranch(message.id, 1)}
													type="button">
													›
												</button>
											</div>
										) : null
									if (!showAssistantActions && !showUserActions && !branchControls) {
										return null
									}
									return (
										<div
											className={`-mt-1 flex items-center gap-2 ${message.role === "user" ? "ml-auto" : ""}`}>
											{branchControls}
											{showAssistantActions && (
												<MessageActions className="opacity-0 transition-opacity group-hover:opacity-100">
													<MessageAction
														label="Copy text"
														onClick={() => handleCopy(message)}
														tooltip={copiedId === message.id ? "Copied" : "Copy text"}>
														{copiedId === message.id ? (
															<Check className="h-3.5 w-3.5" />
														) : (
															<Copy className="h-3.5 w-3.5" />
														)}
													</MessageAction>
													<MessageAction
														label="Copy with tool I/O"
														onClick={() => handleCopy(message, true)}
														tooltip="Copy with tool I/O">
														<TerminalIcon className="h-3.5 w-3.5" />
													</MessageAction>
													<MessageAction
														disabled={status === "streaming"}
														label="Regenerate response"
														onClick={() => handleRegenerate(message.id)}
														tooltip="Regenerate">
														<RefreshCw className="h-3.5 w-3.5" />
													</MessageAction>
												</MessageActions>
											)}
											{showUserActions && (
												<MessageActions className="opacity-0 transition-opacity group-hover:opacity-100">
													<MessageAction
														label="Copy message"
														onClick={() => handleCopy(message)}
														tooltip={copiedId === message.id ? "Copied" : "Copy"}>
														{copiedId === message.id ? (
															<Check className="h-3.5 w-3.5" />
														) : (
															<Copy className="h-3.5 w-3.5" />
														)}
													</MessageAction>
													<MessageAction
														label="Edit message"
														onClick={() => handleEdit(message.id)}
														tooltip="Edit">
														<Pencil className="h-3.5 w-3.5" />
													</MessageAction>
													<MessageAction
														label="Retry message"
														onClick={() => handleRetry(message.id)}
														tooltip="Retry">
														<RefreshCw className="h-3.5 w-3.5" />
													</MessageAction>
												</MessageActions>
											)}
											<span
												className="text-muted-foreground text-xs"
												title={new Date(message.createdAt * 1000).toLocaleString()}>
												{formatRelative(message.createdAt)}
											</span>
										</div>
									)
								})()}
							</Message>
							</div>
						)
					})}
					{error && (
						<p className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-destructive text-sm">
							{error}
						</p>
					)}
				</ConversationContent>
				<ConversationScrollButton />
			</Conversation>

			<div className="sticky bottom-0 shrink-0 border-t bg-background pt-4">
				<PromptInput onSubmit={handleSubmit}>
					<PromptInputBody>
						<PromptInputTextarea
							onChange={(e) => setInput(e.target.value)}
							placeholder={
								agentName === ""
									? "Loading agent…"
									: `Message ${agentName}… (⌘K to focus, ↑ to edit last)`
							}
							ref={composerRef}
							value={input}
						/>
					</PromptInputBody>
					<PromptInputFooter>
						<PromptInputTools />
						<span className="ml-auto select-none font-mono text-muted-foreground text-xs">
							~{estimateTokens(input)} tok
						</span>
						{status === "streaming" || status === "submitted" ? (
							<Button
								onClick={(e) => {
									e.preventDefault()
									stopStreaming()
								}}
								size="sm"
								title="Stop (Esc)"
								type="button"
								variant="outline">
								<Square className="mr-2 h-3.5 w-3.5" />
								Stop
							</Button>
						) : (
							<PromptInputSubmit
								disabled={!input.trim() || agentName === ""}
								status={status}
							/>
						)}
					</PromptInputFooter>
				</PromptInput>
			</div>
		</div>
	)
}

// CompactToolRow renders one tool call as a single line with a
// status dot and the input preview. Click expands to show the full
// input/output payload via the heavier <Tool> components. The
// status colour is heuristic — see classifyToolStatus.
interface CompactToolRowProps {
	part: Extract<Part, { kind: "tool" }>
	densityCompact: boolean
}

function CompactToolRow({ part, densityCompact }: CompactToolRowProps) {
	const status = classifyToolStatus(part)
	const summary = summarizeToolInput(part.input)
	const dotClass =
		status === "running"
			? "text-amber-500"
			: status === "error"
				? "text-destructive"
				: "text-emerald-500"
	// Default-open running tools so the user sees what's executing
	// live; everything else collapses by default. densityCompact
	// forces every row closed regardless.
	const defaultOpen = !densityCompact && status === "running"
	return (
		<details
			className="group/tool rounded-md border bg-muted/30 text-xs"
			key={part.id}
			open={defaultOpen}>
			<summary className="flex cursor-pointer list-none items-center gap-2 px-2 py-1.5 font-mono hover:bg-muted/50">
				{status === "running" ? (
					<Loader2 className={`h-3.5 w-3.5 shrink-0 animate-spin ${dotClass}`} />
				) : status === "error" ? (
					<XIcon className={`h-3.5 w-3.5 shrink-0 ${dotClass}`} />
				) : (
					<Check className={`h-3.5 w-3.5 shrink-0 ${dotClass}`} />
				)}
				<span className="font-semibold">{part.name}</span>
				{summary && (
					<span className="truncate text-muted-foreground">{summary}</span>
				)}
				<Circle className="ml-auto h-2 w-2 shrink-0 opacity-0 transition-opacity group-hover/tool:opacity-50" />
			</summary>
			<div className="space-y-2 border-t bg-background/50 p-2">
				{part.input !== undefined && <ToolInput input={part.input} />}
				<ToolOutput errorText={undefined} output={part.output} />
			</div>
		</details>
	)
}
