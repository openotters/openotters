"use client"

import { createClient, type Transport } from "@connectrpc/connect"
import { useQuery, useTransport } from "@connectrpc/connect-query"
import {
	ArrowLeft,
	Bot,
	Check,
	ChevronDown,
	ChevronRight,
	Copy,
	Loader2,
	Maximize2,
	Pencil,
	RefreshCw,
	Square,
	Terminal as TerminalIcon,
	X as XIcon,
} from "lucide-react"
import Link from "next/link"
import { useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"
import { ConversationEmptyState } from "@/components/ai-elements/conversation"
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
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { listAgents, listSessionMessages } from "@/lib/proto/v1/daemon-Runtime_connectquery"
import { SessionJobsStrip } from "@/components/jobs/session-jobs-strip"
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

// classifyToolStatus reports running / ok / error for a tool row.
// The runtime's tool events don't (yet) carry an exit code, so we
// heuristically inspect the output payload. The shell BIN wraps
// every result with <exit_code>N</exit_code>, which trumps every
// other check — N == 0 is success even if "error" appears in
// stdout (e.g. a grep-for-error log inspection), and N != 0 is
// failure even if the output looks clean.
type ToolStatus = "running" | "ok" | "error"

const EXIT_CODE_RE = /<exit_code>(-?\d+)<\/exit_code>/i

function classifyToolStatus(p: Extract<Part, { kind: "tool" }>): ToolStatus {
	if (p.state === "input-available") return "running"
	const raw = p.output ?? ""

	// Highest-confidence signal: an explicit exit-code tag.
	const m = EXIT_CODE_RE.exec(raw)
	if (m) {
		return m[1] === "0" ? "ok" : "error"
	}

	// Fallback heuristic for tools that don't wrap exit codes.
	// Conservative: only flag a real failure signature. Lines like
	// "ls: cannot access '/proc': Permission denied" appear in
	// successful ls -R runs and shouldn't paint the row red.
	const out = raw.trimStart().toLowerCase()
	if (
		out.startsWith("error:") ||
		out.startsWith("fatal:") ||
		out.startsWith("panic:") ||
		out.includes("traceback (most recent call last)")
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

// parsePersistedParts decodes the runtime's stored assistant
// content JSON (an array of {kind, text, name, input, output,
// state} objects) into the in-memory Part shape the UI renders.
// Falls back to a single text part if content isn't valid JSON
// — handles legacy / malformed rows gracefully.
interface StoredPart {
	kind: string
	text?: string
	tool_id?: string
	name?: string
	input?: string
	output?: string
	state?: string
}

function parsePersistedParts(content: string): Part[] {
	if (!content) return []
	try {
		const arr = JSON.parse(content) as StoredPart[]
		if (!Array.isArray(arr)) return [{ kind: "text", content }]
		const parts: Part[] = []
		for (let i = 0; i < arr.length; i++) {
			const p = arr[i]
			if (p.kind === "text" && typeof p.text === "string") {
				parts.push({ kind: "text", content: p.text })
				continue
			}
			if (p.kind === "tool") {
				let parsedInput: unknown = p.input
				if (typeof p.input === "string" && p.input.length > 0) {
					try {
						parsedInput = JSON.parse(p.input)
					} catch {
						parsedInput = p.input
					}
				}
				parts.push({
					kind: "tool",
					id: p.tool_id || `${p.name ?? "tool"}-${i}`,
					name: p.name ?? "tool",
					input: parsedInput,
					output: p.output ?? null,
					state: p.state === "input-available" ? "input-available" : "output-available",
				})
			}
		}
		return parts
	} catch {
		return [{ kind: "text", content }]
	}
}

// parsePersistedBranches decodes the branches_json column — an
// outer JSON array of inner JSON-encoded parts arrays (each entry
// is itself the same shape as the assistant content column).
function parsePersistedBranches(branchesJSON: string): Part[][] {
	if (!branchesJSON) return []
	try {
		const arr = JSON.parse(branchesJSON) as unknown[]
		if (!Array.isArray(arr)) return []
		const out: Part[][] = []
		for (const raw of arr) {
			if (typeof raw === "string") {
				out.push(parsePersistedParts(raw))
			} else if (Array.isArray(raw)) {
				// Already-decoded array of stored parts — re-stringify
				// then go through the parser to share the same shape.
				out.push(parsePersistedParts(JSON.stringify(raw)))
			}
		}
		return out
	} catch {
		return []
	}
}

// Some models (claude-haiku notably) leak their tool-call protocol
// into raw text content instead of emitting proper tool.call /
// tool.result events through the API. Rather than strip (and lose
// the signal) or render verbatim (ugly XML), we parse those
// markup blocks back into Part values so they render as proper
// tool rows. The runtime's real tool events already produce Part
// objects directly via the stream callbacks; this is just a
// rescue path for the in-text markup.
//
// Recognised shapes:
//   <function_calls>NAME</function_calls>
//   <function_calls>{"name":"NAME","input":{...}}</function_calls>
//   <invocation_result>OUTPUT</invocation_result>
//   <tool_use ...>NAME / {"name":...,"input":...}</tool_use>
//   <tool_result>OUTPUT</tool_result>
const CALL_TAGS = ["function_calls", "tool_use"] as const
const RESULT_TAGS = ["invocation_result", "tool_result"] as const
const ANY_MARKUP_RE = new RegExp(
	`<(${[...CALL_TAGS, ...RESULT_TAGS].join("|")})\\b[^>]*>([\\s\\S]*?)<\\/\\1>`,
	"g",
)

// rescuedToolParts walks a text buffer and splits it into a sequence
// of {text, tool-call, tool-result} chunks, returning the resulting
// Parts list. Plain text that doesn't contain markup short-circuits
// back to a single text part.
function rescuedToolParts(text: string, baseKey: string): Part[] {
	if (!ANY_MARKUP_RE.test(text)) return [{ kind: "text", content: text }]

	ANY_MARKUP_RE.lastIndex = 0
	const out: Part[] = []
	let cursor = 0
	let counter = 0
	let pendingCallName = ""
	for (const m of text.matchAll(ANY_MARKUP_RE)) {
		const [full, tag, inner] = m
		const offset = m.index ?? 0
		if (offset > cursor) {
			const before = text.slice(cursor, offset).trim()
			if (before) out.push({ kind: "text", content: before })
		}
		const tagKind = (CALL_TAGS as readonly string[]).includes(tag) ? "call" : "result"
		const trimmed = inner.trim()
		// Try to parse JSON-shaped tool-use payloads first.
		let parsedName = ""
		let parsedInput: unknown
		if (trimmed.startsWith("{")) {
			try {
				const obj = JSON.parse(trimmed) as { name?: string; input?: unknown }
				if (typeof obj.name === "string") parsedName = obj.name
				parsedInput = obj.input
			} catch {
				// fall through to plain-text handling
			}
		}
		if (tagKind === "call") {
			counter += 1
			const name = parsedName || trimmed || "tool"
			pendingCallName = name
			out.push({
				kind: "tool",
				id: `${baseKey}-rescue-${counter}`,
				name,
				input: parsedInput ?? (parsedName ? undefined : trimmed),
				output: null,
				state: "input-available",
			})
		} else {
			// Result without a preceding call — synthesise an
			// orphan tool block so the output still surfaces.
			const last = out[out.length - 1]
			if (
				last &&
				last.kind === "tool" &&
				last.state === "input-available"
			) {
				out[out.length - 1] = {
					...last,
					output: trimmed,
					state: "output-available",
				}
			} else {
				counter += 1
				out.push({
					kind: "tool",
					id: `${baseKey}-rescue-${counter}`,
					name: pendingCallName || "tool",
					input: undefined,
					output: trimmed,
					state: "output-available",
				})
			}
		}
		cursor = offset + full.length
	}
	if (cursor < text.length) {
		const tail = text.slice(cursor).trim()
		if (tail) out.push({ kind: "text", content: tail })
	}
	return out
}

// expandRescuedParts walks parts top-to-bottom and replaces any text
// part containing tool-call markup with the parsed sequence. Plain
// text passes through untouched.
function expandRescuedParts(parts: Part[], baseKey: string): Part[] {
	let needsExpand = false
	for (const p of parts) {
		if (p.kind === "text" && ANY_MARKUP_RE.test(p.content)) {
			ANY_MARKUP_RE.lastIndex = 0
			needsExpand = true
			break
		}
		ANY_MARKUP_RE.lastIndex = 0
	}
	if (!needsExpand) return parts
	const out: Part[] = []
	for (let i = 0; i < parts.length; i++) {
		const p = parts[i]
		if (p.kind !== "text") {
			out.push(p)
			continue
		}
		out.push(...rescuedToolParts(p.content, `${baseKey}-${i}`))
	}
	return out
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
	const bottomRef = useRef<HTMLDivElement | null>(null)
	// Re-render every minute so relative timestamps tick. Cheap; the
	// state value itself isn't used.
	const [, setNow] = useState(Date.now())
	useEffect(() => {
		const i = setInterval(() => setNow(Date.now()), 60_000)
		return () => clearInterval(i)
	}, [])

	// Auto-scroll the messages section to the bottom whenever a
	// new message lands or the streaming buffer grows. The bottomRef
	// sits at the end of the messages container which has its own
	// overflow-y-auto; only that section scrolls.
	useEffect(() => {
		bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" })
	}, [messages])

	// Hide the layout's app-level header + footer while chat is
	// mounted; the chat owns the full viewport height. We poke
	// data-attribute hooks set in app/layout.tsx rather than a
	// route-aware nested layout because chat is one of many
	// children of the same RootLayout — flipping a class on mount
	// is the smallest change that restores them on every other
	// route. CSS-only via [hidden] is the simplest reversible mark.
	useEffect(() => {
		const header = document.querySelector<HTMLElement>("[data-layout-header]")
		const footer = document.querySelector<HTMLElement>("[data-layout-footer]")
		header?.setAttribute("hidden", "")
		footer?.setAttribute("hidden", "")
		return () => {
			header?.removeAttribute("hidden")
			footer?.removeAttribute("hidden")
		}
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
			.map((m, i) => {
				const role = m.role as "user" | "assistant"
				if (role === "user") {
					return {
						id: `hist-${i}-user`,
						role,
						branches: [[{ kind: "text", content: m.content } satisfies Part]],
						activeBranch: 0,
						createdAt: Number(m.createdAt),
					}
				}
				// Assistant: content is JSON parts; branchesJson is
				// JSON-encoded array of alternative parts arrays.
				const active = parsePersistedParts(m.content)
				const others = parsePersistedBranches(m.branchesJson ?? "")
				const all: Part[][] =
					m.activeBranch != null && others.length > 0
						? // activeBranch points at one slot in
							// [..others, active] in append-order (the
							// most recent run is always the new
							// content; older alternatives sit in
							// branches_json). Reconstruct: place the
							// active slot at activeBranch.
							(() => {
								const seq = [...others, active]
								return seq
							})()
						: [active]
				return {
					id: `hist-${i}-assistant`,
					role,
					branches: all,
					activeBranch:
						m.activeBranch != null && m.activeBranch >= 0 && m.activeBranch < all.length
							? m.activeBranch
							: all.length - 1,
					createdAt: Number(m.createdAt),
				}
			})
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
				{ ref: agentName, sessionId, prompt, regenerate: mode === "regenerate" },
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
		// Three-row chat:
		//   1. fixed header (shrink-0)
		//   2. messages (flex-1 + overflow-y-auto, the only scroll)
		//   3. fixed composer (shrink-0)
		//
		// The chat hides the app-level header/footer (see the
		// [hidden] toggle above) and claims the full viewport.
		// Negative margins reach across main's p-6 padding so
		// the bars span edge-to-edge.
		<div className="-mx-6 -my-6 flex h-[100dvh] flex-col overflow-hidden">
			<div className="flex shrink-0 items-center gap-4 border-b bg-background px-6 py-4">
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

			<SessionJobsStrip sessionId={sessionId} />

			<div className="min-h-0 flex-1 overflow-y-auto px-6 py-6">
				<div className="flex flex-col gap-8">
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
						const rawParts = message.branches[message.activeBranch] ?? []
						// Rescue any tool-call markup the model leaked into
						// text content; runtime-emitted tool events already
						// arrive as proper Parts and pass through untouched.
						const activeParts = expandRescuedParts(
							rawParts,
							`${message.id}-b${message.activeBranch}`,
						)
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
											if (g.content.trim() === "") return null
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
				</div>
				<div ref={bottomRef} />
			</div>

			<div className="shrink-0 border-t bg-background px-6 py-4">
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

// Tool output lengths can be tens of KB (kubectl describe, log
// dumps, raw JSON). Inline-render up to TOOL_OUTPUT_INLINE_LIMIT
// characters; above that we show a tail snippet + a "Show full
// output" button that opens a dialog. Same applies to tool input
// JSON for symmetry.
const TOOL_OUTPUT_INLINE_LIMIT = 2000

function clipForInline(s: string, limit: number): { shown: string; clipped: boolean } {
	if (s.length <= limit) return { shown: s, clipped: false }
	// Take the tail — for log / kubectl-style output, the bottom
	// is usually the part the user wants. Round to a newline so we
	// don't cut a column mid-row.
	const tail = s.slice(s.length - limit)
	const nl = tail.indexOf("\n")
	const shown = nl >= 0 ? tail.slice(nl + 1) : tail
	return { shown, clipped: true }
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
	const [fullOpen, setFullOpen] = useState(false)
	const [copiedFull, setCopiedFull] = useState(false)
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

	const inputJSON =
		part.input === undefined ? "" : JSON.stringify(part.input, null, 2)
	const inputClip = clipForInline(inputJSON, TOOL_OUTPUT_INLINE_LIMIT)
	const outputClip = clipForInline(part.output ?? "", TOOL_OUTPUT_INLINE_LIMIT)
	const hasClipped = inputClip.clipped || outputClip.clipped

	const copyFull = async () => {
		const blob = [
			`# ${part.name}`,
			"## input",
			inputJSON || "(none)",
			"## output",
			part.output ?? "(none)",
		].join("\n\n")
		try {
			await navigator.clipboard.writeText(blob)
			setCopiedFull(true)
			setTimeout(() => setCopiedFull(false), 1500)
		} catch (err) {
			toast.error("Copy failed", {
				description: err instanceof Error ? err.message : String(err),
			})
		}
	}

	return (
		<>
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
					{hasClipped && (
						<span className="shrink-0 rounded bg-amber-500/15 px-1.5 py-0.5 font-medium text-amber-600 text-[10px] dark:text-amber-400">
							clipped
						</span>
					)}
					<button
						aria-label="Show call & result in modal"
						className="ml-auto inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-muted-foreground text-xs hover:bg-muted hover:text-foreground"
						onClick={(e) => {
							// Don't toggle <details> when the user clicks
							// the modal button — they're separate actions.
							e.preventDefault()
							e.stopPropagation()
							setFullOpen(true)
						}}
						title="Open call + result in modal"
						type="button">
						<Maximize2 className="h-3 w-3" />
						<span className="sr-only">Details</span>
					</button>
				</summary>
				<div className="space-y-2 border-t bg-background/50 p-2">
					{part.input !== undefined && (
						<div className="space-y-1">
							{inputClip.clipped && (
								<p className="text-muted-foreground text-[10px]">
									input clipped — showing last {TOOL_OUTPUT_INLINE_LIMIT} chars of{" "}
									{inputJSON.length}
								</p>
							)}
							<ToolInput input={inputClip.clipped ? inputClip.shown : part.input} />
						</div>
					)}
					{(part.output !== null || status === "running") && (
						<div className="space-y-1">
							{outputClip.clipped && (
								<p className="text-muted-foreground text-[10px]">
									output clipped — showing last {TOOL_OUTPUT_INLINE_LIMIT} chars of{" "}
									{(part.output ?? "").length}
								</p>
							)}
							<ToolOutput
								errorText={undefined}
								output={outputClip.clipped ? outputClip.shown : part.output}
							/>
						</div>
					)}
					{hasClipped && (
						<div className="flex justify-end">
							<Button
								onClick={() => setFullOpen(true)}
								size="sm"
								variant="outline">
								Show full output
							</Button>
						</div>
					)}
				</div>
			</details>
			<Dialog onOpenChange={setFullOpen} open={fullOpen}>
				<DialogContent className="flex max-h-[85vh] max-w-4xl flex-col gap-0 overflow-hidden p-0">
					<DialogHeader className="border-b px-6 pt-6 pb-3 pr-12">
						<div className="flex items-start justify-between gap-4">
							<div className="min-w-0">
								<DialogTitle className="font-mono">{part.name}</DialogTitle>
								<DialogDescription>
									{inputJSON.length} char input · {(part.output ?? "").length} char
									output
								</DialogDescription>
							</div>
							<Button
								className="shrink-0"
								onClick={copyFull}
								size="sm"
								variant="outline">
								{copiedFull ? (
									<Check className="mr-2 h-3.5 w-3.5" />
								) : (
									<Copy className="mr-2 h-3.5 w-3.5" />
								)}
								{copiedFull ? "Copied" : "Copy all"}
							</Button>
						</div>
					</DialogHeader>
					<div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-6 py-4 font-mono text-xs">
						<section>
							<h3 className="mb-1 font-semibold text-muted-foreground text-[10px] uppercase tracking-wide">
								Input
							</h3>
							<pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-md border bg-muted/40 p-3">
								{inputJSON || "(none)"}
							</pre>
						</section>
						<section>
							<h3 className="mb-1 font-semibold text-muted-foreground text-[10px] uppercase tracking-wide">
								Output
							</h3>
							<pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-md border bg-muted/40 p-3">
								{part.output ?? "(none)"}
							</pre>
						</section>
					</div>
				</DialogContent>
			</Dialog>
		</>
	)
}
