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
	ExternalLink,
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
import {
	getAsyncJob,
	listAgents,
	listSessionMessages,
} from "@/lib/proto/v1/daemon-Runtime_connectquery"
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

// formatDuration renders ms as the smallest sensible unit:
//   < 1s   → "843ms"
//   < 60s  → "12.3s"
//   ≥ 60s  → "1m02s"
function formatDuration(ms: number): string {
	if (ms < 1000) return `${ms}ms`
	const s = ms / 1000
	if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)}s`
	const m = Math.floor(s / 60)
	const rem = Math.floor(s % 60)
	return `${m}m${rem.toString().padStart(2, "0")}s`
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
			// startedAt is the unix-millis timestamp captured when
			// the tool.call event lands. Used by CompactToolRow to
			// render an elapsed counter while state stays
			// "input-available" (running) — so a long job_wait shows
			// "running 12s" instead of just an inert spinner.
			startedAt?: number
			// durationMs is the runtime-measured wall-clock time
			// from OnToolCall to OnToolResult. Set when the result
			// lands (tool.result event) and preserved across page
			// refreshes via the persisted StoredPart.duration_ms
			// field. CompactToolRow renders "took Xs" once it's
			// known.
			durationMs?: number
	  }

interface UIMessage {
	id: string
	// "system" is the runtime's out-of-band compaction notice — not a
	// user or assistant turn, rendered as a centered divider with an
	// expand-for-summary button instead of a bubble.
	role: "user" | "assistant" | "system"
	// Branches let the user regenerate without losing the prior
	// answer: each call to "Regenerate" appends a new array of parts
	// and bumps activeBranch to it; the message UI exposes prev/next
	// to flip between them. branches[activeBranch] is what renders.
	// Empty for system rows.
	branches: Part[][]
	activeBranch: number
	createdAt: number // unix seconds
	failed?: boolean // assistant turn that errored — surfaces a Retry CTA
	// compaction is set only when role === "system". `text` is the
	// runtime's notice line ("Memory compacted (summarized). N older
	// messages were condensed into a summary above."); `summary` is
	// the linked assistant message body ("[Conversation summary]: …"),
	// hidden by default and revealed by the row's toggle.
	compaction?: { text: string; summary?: string }
}

// Patterns produced by runtime/pkg/memory/compact.go. Stable under
// our control — string match is fine.
const COMPACTION_NOTICE_RE = /^\[system\]:\s*Memory compacted/
const CONVERSATION_SUMMARY_RE = /^\[Conversation summary\]:\s*/

// firstTextContent returns the first text part's content for a
// hydrated UIMessage, or "" when the active branch has no text.
function firstTextContent(m: UIMessage): string {
	const parts = m.branches[m.activeBranch] ?? []
	for (const p of parts) {
		if (p.kind === "text") return p.content
	}
	return ""
}

// applyCompactionMarkers folds compactor-emitted bookkeeping into a
// single non-bubble system row. The compactor (memory/compact.go)
// stores:
//   - one assistant message: "[Conversation summary]: <summary>"
//     (summarize strategy only)
//   - one trailing user message: "[system]: Memory compacted (…). …"
// Both render as ordinary bubbles by default, which is wrong — the
// user didn't say "[system]: …" and the summary isn't a model reply
// they wrote. Collapse to one system row that owns the disclosure.
function applyCompactionMarkers(messages: UIMessage[]): UIMessage[] {
	let summaryIdx = -1
	let summaryText = ""
	for (let i = 0; i < messages.length; i++) {
		const m = messages[i]
		if (m.role !== "assistant") continue
		const t = firstTextContent(m)
		if (CONVERSATION_SUMMARY_RE.test(t)) {
			summaryIdx = i
			summaryText = t.replace(CONVERSATION_SUMMARY_RE, "")
			break
		}
	}

	const out: UIMessage[] = []
	for (let i = 0; i < messages.length; i++) {
		if (i === summaryIdx) continue
		const m = messages[i]
		if (m.role === "user") {
			const t = firstTextContent(m)
			if (COMPACTION_NOTICE_RE.test(t)) {
				out.push({
					id: m.id,
					role: "system",
					branches: [],
					activeBranch: 0,
					createdAt: m.createdAt,
					compaction: {
						text: t.replace(/^\[system\]:\s*/, ""),
						summary: summaryIdx >= 0 ? summaryText : undefined,
					},
				})
				continue
			}
		}
		out.push(m)
	}
	return out
}

// CompactionDivider renders the runtime's "memory compacted" notice
// as a centered system row rather than a chat bubble. The summary
// (when present) sits behind a disclosure toggle so the conversation
// timeline stays uncluttered for users who don't need to look.
function CompactionDivider({
	compaction,
}: {
	compaction: { text: string; summary?: string }
}) {
	const [open, setOpen] = useState(false)
	const hasSummary = Boolean(compaction.summary)
	return (
		<div className="my-4 select-text">
			<div className="flex items-center gap-3 text-muted-foreground text-xs">
				<div className="h-px flex-1 bg-border" />
				<div className="flex items-center gap-2">
					<span className="font-medium uppercase tracking-wider">
						{compaction.text}
					</span>
					{hasSummary && (
						<Button
							className="h-6 gap-1 px-2 text-xs"
							onClick={() => setOpen((v) => !v)}
							size="sm"
							type="button"
							variant="outline">
							{open ? (
								<ChevronDown className="h-3 w-3" />
							) : (
								<ChevronRight className="h-3 w-3" />
							)}
							{open ? "Hide summary" : "Show summary"}
						</Button>
					)}
				</div>
				<div className="h-px flex-1 bg-border" />
			</div>
			{open && hasSummary && (
				<div className="mx-auto mt-3 max-w-[80%] rounded-lg border bg-muted/40 p-3 text-foreground text-sm">
					<div className="mb-1 font-medium text-muted-foreground text-xs uppercase tracking-wider">
						Conversation summary
					</div>
					<div className="whitespace-pre-wrap break-words">
						{compaction.summary}
					</div>
				</div>
			)}
		</div>
	)
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
	duration_ms?: number
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
					durationMs: typeof p.duration_ms === "number" ? p.duration_ms : undefined,
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
			startedAt: Date.now(),
		},
	]
}

// attachToolResult flips the most recent still-running tool call with
// the matching name to "output-available" and records the result.
// We match on name because the daemon's ChatStreamEvent already
// carries the tool_id but the older code didn't thread it through;
// name-match still works because tool calls within a single turn
// don't repeat without the model regenerating. durationMs is the
// runtime-measured wall-clock for the tool execution.
function attachToolResult(
	parts: Part[], name: string, content: string, durationMs?: number,
): Part[] {
	const next = parts.slice()
	for (let i = next.length - 1; i >= 0; i--) {
		const p = next[i]
		if (p.kind === "tool" && p.name === name && p.state === "input-available") {
			next[i] = { ...p, output: content, state: "output-available", durationMs }
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
			durationMs,
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

	// streamLabel is the placeholder line shown while the latest
	// assistant turn has no parts yet. Updates on step.start /
	// tool.call signals so the user sees "Step 2 — calling
	// job_wait…" instead of staying on "Thinking…" through a long
	// streamed tool_use block.
	const [streamLabel, setStreamLabel] = useState("Thinking…")
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
	// Auto-scroll is delegated to <Conversation> from ai-elements
	// (wraps use-stick-to-bottom). It handles "stick to tail unless
	// the user scrolls up" and the ConversationScrollButton appears
	// when the user is detached from the bottom.
	// Re-render every minute so relative timestamps tick. Cheap; the
	// state value itself isn't used.
	const [, setNow] = useState(Date.now())
	useEffect(() => {
		const i = setInterval(() => setNow(Date.now()), 60_000)
		return () => clearInterval(i)
	}, [])


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
		// Race guard: if the user already started a turn (or the
		// stream reducer is mid-flight) before history.data resolved,
		// don't clobber the live state with the persisted snapshot.
		// Mark hydration "done" anyway so a later refetch doesn't
		// retry this branch.
		if (messages.length > 0 || status === "submitted" || status === "streaming") {
			hydratedRef.current = key
			return
		}
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
			setMessages(applyCompactionMarkers(persisted))
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
			setStreamLabel("Thinking…")

			for await (const event of stream) {
				// Update the placeholder label first so a long
				// streamed tool_use block (no text deltas, no
				// finalised tool.call yet) still surfaces something.
				if (event.type === "step.start") {
					setStreamLabel(
						event.step > 1
							? `Working — step ${event.step}…`
							: "Working…",
					)
				} else if (event.type === "tool.call") {
					setStreamLabel(`Calling ${event.tool}…`)
				}

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
									attachToolResult(
										parts, event.tool, event.content,
										event.durationMs ? Number(event.durationMs) : undefined,
									),
								)
							// step.start / step.finish drive streamLabel
							// (handled above) and don't add parts.
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
								className={`absolute right-0.5 bottom-0.5 inline-flex h-2.5 w-2.5 rounded-full ring-2 ring-background ${
									agent.status === "ready"
										? "bg-emerald-500"
										: agent.status === "working"
											? "bg-blue-500 animate-pulse"
											: agent.status === "pulling" || agent.status === "starting"
												? "bg-amber-500 animate-pulse"
												: agent.status === "failed"
													? "bg-red-500"
													: "bg-muted-foreground/40"
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

			<Conversation className="min-h-0 flex-1">
				<ConversationContent className="px-6 py-6">
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
						if (message.role === "system" && message.compaction) {
							return (
								<CompactionDivider
									compaction={message.compaction}
									key={message.id}
								/>
							)
						}
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
										<Shimmer className="text-sm">{streamLabel}</Shimmer>
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
												className="text-muted-foreground text-xs opacity-0 transition-opacity group-hover:opacity-100"
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

// isAgentCallTool reports whether a tool row should render as a
// cross-agent conversation panel (prompt + response bubbles) rather
// than the generic input/output JSON view. agent_exec wraps a
// {ref, prompt, session_id?} → {response, session_id} round-trip
// and benefits from a chat-shaped renderer.
//
// (agent_chat used to be a separate threaded variant; alpha.85
// folded it into agent_exec — pass a session_id to preserve
// history, omit it for a fresh thread.)
function isAgentCallTool(name: string): boolean {
	return name === "agent_exec"
}

// AgentCallPanel renders an agent_exec round-trip as a mini-
// conversation: a "you said" bubble for the prompt the caller
// sent and a "they replied" bubble for the target's response.
// The session id sits in the footer so the operator can match
// the row to the target's session view at a glance — the
// CrossAgentLink button below wraps the same id into a clickable
// jump. When the caller passed a session_id in the input, the
// panel shows "threaded" — multiple agent_exec calls on the same
// session preserve history on the target.
function AgentCallPanel({
	input,
	output,
	running,
}: {
	input: unknown
	output: string | null
	running: boolean
}) {
	const ref = parseAgentRef(input)
	const prompt = parseAgentPrompt(input)
	const inputSession = parseInputSessionID(input)
	const { response, sessionID } = parseAgentOutput(output)
	const effectiveSession = sessionID || inputSession

	return (
		<div className="space-y-2">
			<div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
				<span className="rounded bg-muted px-1.5 py-0.5 font-mono">
					→ {ref || "(no ref)"}
				</span>
				{effectiveSession && (
					<span className="rounded bg-muted px-1.5 py-0.5 font-mono">
						session: {effectiveSession.length > 28
							? `${effectiveSession.slice(0, 14)}…${effectiveSession.slice(-10)}`
							: effectiveSession}
					</span>
				)}
				<span className="rounded bg-muted/60 px-1.5 py-0.5">
					{inputSession ? "threaded (continues session)" : "new thread"}
				</span>
			</div>
			<AgentBubble role="prompt" text={prompt || "(empty prompt)"} />
			{running ? (
				<p className="rounded-md border bg-muted/30 px-2 py-1.5 text-[11px] text-muted-foreground italic">
					Waiting for {ref || "target"} to reply…
				</p>
			) : (
				<AgentBubble role="response" text={response || "(empty response)"} />
			)}
		</div>
	)
}

// AgentBubble renders one side of the cross-agent exchange — a
// labelled block with role-tinted styling so the operator can
// scan the prompt-vs-response at a glance. Markdown / code is
// left as monospace text so multi-line content (jq filters,
// kubectl output) stays legible.
function AgentBubble({ role, text }: { role: "prompt" | "response"; text: string }) {
	const isPrompt = role === "prompt"
	return (
		<div
			className={`rounded-md border px-2 py-1.5 ${
				isPrompt ? "border-primary/30 bg-primary/5" : "border-emerald-500/30 bg-emerald-500/5"
			}`}
		>
			<div
				className={`mb-1 text-[10px] font-semibold uppercase tracking-wide ${
					isPrompt ? "text-primary" : "text-emerald-700 dark:text-emerald-400"
				}`}
			>
				{isPrompt ? "Prompt" : "Response"}
			</div>
			<pre className="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed">
				{text}
			</pre>
		</div>
	)
}

function parseAgentPrompt(input: unknown): string {
	if (!input || typeof input !== "object") return ""
	const v = (input as Record<string, unknown>).prompt
	return typeof v === "string" ? v : ""
}

function parseInputSessionID(input: unknown): string {
	if (!input || typeof input !== "object") return ""
	const v = (input as Record<string, unknown>).session_id
	return typeof v === "string" ? v : ""
}

function parseAgentOutput(output: string | null): { response: string; sessionID: string } {
	if (!output) return { response: "", sessionID: "" }
	try {
		const parsed = JSON.parse(output) as { response?: unknown; session_id?: unknown }
		return {
			response: typeof parsed.response === "string" ? parsed.response : "",
			sessionID: typeof parsed.session_id === "string" ? parsed.session_id : "",
		}
	} catch {
		// agent_exec returns a raw string. Treat it as the response
		// body and leave session_id empty (the daemon wipes the exec
		// session anyway).
		return { response: output, sessionID: "" }
	}
}

// CrossAgentLink surfaces a "view conversation in <target>" jump
// for agent_exec tool rows. The daemon's AgentExec handler always
// returns a session_id (mints a self-describing one if the caller
// didn't supply it), so the link always resolves to the target's
// own session view — the operator can read the full thread
// including any follow-up agent_exec calls the orchestrator made
// on the same session.
function CrossAgentLink({
	toolName,
	input,
	output,
}: {
	toolName: string
	input: unknown
	output: string
}) {
	if (toolName !== "agent_exec") return null
	const targetRef = parseAgentRef(input)
	if (!targetRef) return null
	// Prefer the session id the target persisted under (output);
	// fall back to whatever the caller supplied (input) so the
	// link still resolves when output parsing fails for any
	// reason.
	const sessionID = parseSessionID(output) || parseInputSessionID(input)
	const href = sessionID
		? `/agents/${encodeURIComponent(targetRef)}/chat/${encodeURIComponent(sessionID)}`
		: `/agents/${encodeURIComponent(targetRef)}`
	return (
		<div className="flex items-center justify-end gap-2 pt-1">
			<Link
				className="inline-flex items-center gap-1 rounded border bg-muted/40 px-2 py-1 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"
				href={href}
			>
				<ExternalLink className="h-3 w-3" />
				View conversation in {targetRef}
			</Link>
		</div>
	)
}

function parseAgentRef(input: unknown): string {
	if (!input || typeof input !== "object") return ""
	const v = (input as Record<string, unknown>).ref
	return typeof v === "string" ? v : ""
}

function parseSessionID(output: string): string {
	try {
		const parsed = JSON.parse(output) as { session_id?: unknown }
		if (typeof parsed.session_id === "string") return parsed.session_id
	} catch {
		// agent_exec returns a plain string, not JSON — no
		// session id available. Fall through.
	}
	return ""
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
	// Tick once a second while the tool is running so the elapsed
	// counter under the row name advances live (job_wait can sit
	// for minutes; the user shouldn't wonder whether the daemon's
	// hung).
	const [elapsed, setElapsed] = useState(() =>
		part.startedAt ? Math.max(0, Math.floor((Date.now() - part.startedAt) / 1000)) : 0,
	)
	useEffect(() => {
		if (status !== "running" || !part.startedAt) return
		const tick = () => setElapsed(Math.floor((Date.now() - (part.startedAt ?? 0)) / 1000))
		tick()
		const i = setInterval(tick, 1000)
		return () => clearInterval(i)
	}, [status, part.startedAt])
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
					{status === "running" && part.startedAt && (
						<span className="shrink-0 font-mono text-[10px] text-amber-600 tabular-nums dark:text-amber-400">
							{elapsed >= 60
								? `${Math.floor(elapsed / 60)}m${(elapsed % 60).toString().padStart(2, "0")}s`
								: `${elapsed}s`}
						</span>
					)}
					{status !== "running" && typeof part.durationMs === "number" && part.durationMs > 0 && (
						<span
							className="shrink-0 font-mono text-[10px] text-muted-foreground tabular-nums"
							title={`${part.durationMs} ms`}>
							{formatDuration(part.durationMs)}
						</span>
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
					{/*
					 * agent_chat / agent_exec get a specialised render
					 * instead of the raw JSON parameters / output: the
					 * model's prompt and the target's reply are the
					 * load-bearing content, surfacing them as
					 * conversation bubbles is what the operator wants
					 * to see (raw JSON still available in the
					 * Maximize2 modal). For every other tool, fall
					 * through to the generic ToolInput / ToolOutput.
					 */}
					{isAgentCallTool(part.name) ? (
						<AgentCallPanel
							input={part.input}
							output={part.output}
							running={status === "running"}
						/>
					) : (
						<>
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
						</>
					)}
					{/*
					 * Agent-to-agent shortcut: a one-click link to the
					 * target's session so the operator can read the
					 * full cross-agent thread in the target's own
					 * chat view (the target persists each session
					 * regardless of which agent initiated it).
					 */}
					{part.output && <CrossAgentLink toolName={part.name} input={part.input} output={part.output} />}
					{/*
					 * Live job logs for any job-observing tool row — renders
					 * regardless of tool status. While the tool is running,
					 * the panel polls the row and shows growing stdout/stderr;
					 * once the tool returns, the panel still shows the
					 * authoritative row state (stable for terminal jobs).
					 * The panel is the canonical surface for job_watch in
					 * particular — leaving it out post-refresh would lose
					 * the very thing the user came to see. Polling stops
					 * server-side as soon as the job is terminal.
					 */}
					{jobToolWatchId(part) !== null && (
						<LiveJobLogs jobId={jobToolWatchId(part) ?? ""} />
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
			{fullOpen && (
				// Streamdown's fullscreen pattern: a plain fixed
				// overlay over the viewport, not a Dialog modal.
				// Identical shape to the one Streamdown opens for a
				// table/code-block expand, so both views feel the
				// same.
				<div className="fixed inset-0 z-50 flex flex-col bg-background">
					<div className="flex items-start justify-between gap-4 border-b px-6 pt-6 pb-3">
						<div className="min-w-0">
							<h2 className="font-mono font-semibold">{part.name}</h2>
							<p className="text-muted-foreground text-sm">
								{inputJSON.length} char input · {(part.output ?? "").length} char output
							</p>
						</div>
						<div className="flex shrink-0 items-center gap-2">
							<Button onClick={copyFull} size="sm" variant="outline">
								{copiedFull ? (
									<Check className="mr-2 h-3.5 w-3.5" />
								) : (
									<Copy className="mr-2 h-3.5 w-3.5" />
								)}
								{copiedFull ? "Copied" : "Copy all"}
							</Button>
							<Button
								aria-label="Close fullscreen"
								onClick={() => setFullOpen(false)}
								size="icon"
								variant="ghost">
								<XIcon className="h-4 w-4" />
							</Button>
						</div>
					</div>
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
				</div>
			)}
		</>
	)
}

// JOB_WATCH_TOOLS is the set of agent tools where live stdout/stderr
// is the user-visible point of the call: job_watch is explicitly
// "tail this job's stdout", job_wait is "block on this job's
// completion" (still want to see progress while it runs), and
// job_status returns the row snapshot — the model uses it to peek,
// but the human reading the chat thread also benefits from seeing
// the same growing buffer the snapshot is sampling from.
//
// job_submit / job_list / job_cancel are deliberately omitted —
// they're fire-and-forget against a specific row (submit) or a row
// set (list) / a cancel signal; no streaming buffer to display.
const JOB_WATCH_TOOLS = new Set(["job_watch", "job_wait", "job_status"])

// jobToolWatchId picks the daemon job ID out of a job-observing
// tool's input. The runtime's `job_*` tools all accept a JobIDInput
// (see runtime/pkg/tool/jobs.go: { "job_id": "job_…" }), so the
// lookup is positional and validated against the obvious shape.
// Returns null for any tool that isn't one of the watched names or
// for malformed input — the caller then skips rendering the live
// log pane.
function jobToolWatchId(part: Extract<Part, { kind: "tool" }>): string | null {
	if (!JOB_WATCH_TOOLS.has(part.name)) return null
	const input = part.input as { job_id?: unknown } | null | undefined
	const id = input?.job_id
	if (typeof id !== "string" || id === "") return null
	return id
}

// LIVE_JOB_TERMINAL mirrors the set used on /jobs/[job] — keep them
// in lockstep. Polling stops as soon as the job hits one of these.
const LIVE_JOB_TERMINAL = new Set(["done", "error", "cancelled", "orphaned"])

// LiveJobLogs streams a job's stdout / stderr inside the chat thread
// while the agent is mid-`job_watch` / `job_wait` / `job_status`. It
// piggybacks on the same getAsyncJob endpoint the /jobs/[job] view
// uses — same 1s poll cadence, same column data — so the row
// already gets the growing logs the streaming-sink path persists
// daemon-side. Stops polling as soon as the job is terminal; the
// parent CompactToolRow also unmounts this on tool completion
// (status !== "running").
//
// Always renders a visible panel: for job_watch in particular,
// live stdout is the *point* of the tool call, so hiding the panel
// while the buffer is empty would defeat the affordance. Empty
// stdout/stderr show "(streaming …)" placeholders, matching the
// /jobs/[job] view's behaviour.
function LiveJobLogs({ jobId }: { jobId: string }) {
	const { data } = useQuery(
		getAsyncJob,
		{ jobId },
		{
			enabled: jobId !== "",
			refetchInterval: (query) => {
				const status = query.state.data?.job?.status
				if (status && LIVE_JOB_TERMINAL.has(status)) return false
				return 1_000
			},
		},
	)
	const job = data?.job
	const stdout = job?.stdout ?? ""
	const stderr = job?.stderr ?? ""

	return (
		<div className="space-y-1.5 rounded-md border bg-muted/20 p-2">
			<p className="font-medium text-[10px] text-muted-foreground uppercase tracking-wide">
				live job logs · {jobId}
			</p>
			<div className="space-y-0.5">
				<p className="text-[9px] text-muted-foreground uppercase tracking-wide">stdout</p>
				{stdout !== "" ? (
					<pre className="max-h-48 overflow-y-auto whitespace-pre-wrap rounded bg-background px-2 py-1 font-mono text-[11px]">
						{stdout}
					</pre>
				) : (
					<p className="rounded border border-dashed bg-background/60 px-2 py-1 text-[10px] text-muted-foreground italic">
						streaming stdout…
					</p>
				)}
			</div>
			<div className="space-y-0.5">
				<p className="text-[9px] text-muted-foreground uppercase tracking-wide">stderr</p>
				{stderr !== "" ? (
					<pre className="max-h-48 overflow-y-auto whitespace-pre-wrap rounded bg-background px-2 py-1 font-mono text-[11px]">
						{stderr}
					</pre>
				) : (
					<p className="rounded border border-dashed bg-background/60 px-2 py-1 text-[10px] text-muted-foreground italic">
						streaming stderr…
					</p>
				)}
			</div>
		</div>
	)
}
