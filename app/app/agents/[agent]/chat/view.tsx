"use client"

import { createClient, type Transport } from "@connectrpc/connect"
import { useTransport } from "@connectrpc/connect-query"
import { ArrowLeft, Bot } from "lucide-react"
import Link from "next/link"
import { useMemo, useState } from "react"
import { Streamdown } from "streamdown"
import {
	Conversation,
	ConversationContent,
	ConversationEmptyState,
	ConversationScrollButton,
} from "@/components/ai-elements/conversation"
import { Message, MessageContent } from "@/components/ai-elements/message"
import {
	PromptInput,
	PromptInputBody,
	PromptInputFooter,
	PromptInputSubmit,
	PromptInputTextarea,
	PromptInputTools,
	type PromptInputMessage,
} from "@/components/ai-elements/prompt-input"
import {
	Tool,
	ToolContent,
	ToolHeader,
	ToolInput,
	ToolOutput,
} from "@/components/ai-elements/tool"
import { Button } from "@/components/ui/button"
import { Runtime } from "@/lib/proto/v1/daemon_pb"
import { useRouteParams } from "@/lib/use-route-params"

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
	parts: Part[]
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
	const params = useRouteParams<{ agent: string }>("/agents/:agent/chat")
	const agentName = params.agent ?? ""
	const transport = useTransport() as Transport
	const client = useMemo(() => createClient(Runtime, transport), [transport])

	const [messages, setMessages] = useState<UIMessage[]>([])
	const [input, setInput] = useState("")
	const [status, setStatus] = useState<"ready" | "submitted" | "streaming" | "error">("ready")
	const [error, setError] = useState<string | null>(null)
	const sessionId = useMemo(() => `web:chat:${crypto.randomUUID()}`, [])

	const handleSubmit = async (msg: PromptInputMessage) => {
		const prompt = msg.text.trim()
		if (!prompt || agentName === "" || status === "streaming") {
			return
		}

		const userMessage: UIMessage = {
			id: `msg-${Date.now()}-user`,
			role: "user",
			parts: [{ kind: "text", content: prompt }],
		}

		const assistantId = `msg-${Date.now()}-assistant`
		const assistantSeed: UIMessage = {
			id: assistantId,
			role: "assistant",
			parts: [],
		}

		setMessages((prev) => [...prev, userMessage, assistantSeed])
		setInput("")
		setStatus("submitted")
		setError(null)

		try {
			const stream = client.chatStreamWithAgent({
				ref: agentName,
				sessionId,
				prompt,
			})

			let toolCounter = 0
			setStatus("streaming")

			for await (const event of stream) {
				setMessages((prev) => {
					const next = [...prev]
					const last = next[next.length - 1]
					if (!last || last.role !== "assistant" || last.id !== assistantId) {
						return next
					}

					switch (event.type) {
						case "text.delta":
							next[next.length - 1] = {
								...last,
								parts: pushTextDelta(last.parts, event.content),
							}
							break
						case "tool.call":
							toolCounter += 1
							next[next.length - 1] = {
								...last,
								parts: pushToolCall(
									last.parts,
									`${event.tool}-${toolCounter}`,
									event.tool,
									event.content,
								),
							}
							break
						case "tool.result":
							next[next.length - 1] = {
								...last,
								parts: attachToolResult(last.parts, event.tool, event.content),
							}
							break
						// step.start / step.finish are intentionally dropped —
						// they're per-step bookkeeping that doesn't add visual
						// information once text + tool blocks render in order.
						default:
							break
					}
					return next
				})
			}

			setStatus("ready")
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err))
			setStatus("error")
		}
	}

	return (
		<div className="flex h-[calc(100vh-8rem)] flex-col">
			<div className="flex items-center gap-4 border-b pb-4">
				<Button asChild size="icon" variant="ghost">
					<Link href={`/agents/${agentName}`}>
						<ArrowLeft className="h-4 w-4" />
					</Link>
				</Button>
				<div className="flex items-center gap-3">
					<div className="flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10">
						<Bot className="h-5 w-5 text-primary" />
					</div>
					<div>
						<h1 className="font-semibold">{agentName || "—"}</h1>
						<p className="font-mono text-muted-foreground text-xs">
							session {sessionId.slice(-8)}
						</p>
					</div>
				</div>
			</div>

			<Conversation className="flex-1">
				<ConversationContent>
					{messages.length === 0 && (
						<ConversationEmptyState
							description="Send a message to start the conversation. Streaming text + tool calls render in order."
							icon={<Bot className="h-8 w-8 text-muted-foreground/50" />}
							title="No messages yet"
						/>
					)}
					{messages.map((message) => (
						<Message from={message.role} key={message.id}>
							<MessageContent>
								{message.parts.map((part, idx) => {
									if (part.kind === "text") {
										return (
											<Streamdown key={`${message.id}-t-${idx}`}>{part.content}</Streamdown>
										)
									}
									return (
										<Tool defaultOpen={part.state === "input-available"} key={part.id}>
											<ToolHeader
												state={part.state}
												toolName={part.name}
												type="dynamic-tool"
											/>
											<ToolContent>
												{part.input !== undefined && <ToolInput input={part.input} />}
												<ToolOutput errorText={undefined} output={part.output} />
											</ToolContent>
										</Tool>
									)
								})}
							</MessageContent>
						</Message>
					))}
					{error && (
						<p className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-destructive text-sm">
							{error}
						</p>
					)}
				</ConversationContent>
				<ConversationScrollButton />
			</Conversation>

			<div className="border-t pt-4">
				<PromptInput onSubmit={handleSubmit}>
					<PromptInputBody>
						<PromptInputTextarea
							onChange={(e) => setInput(e.target.value)}
							placeholder={
								agentName === "" ? "Loading agent…" : `Message ${agentName}…`
							}
							value={input}
						/>
					</PromptInputBody>
					<PromptInputFooter>
						<PromptInputTools />
						<PromptInputSubmit
							disabled={!input.trim() || agentName === "" || status === "streaming"}
							status={status}
						/>
					</PromptInputFooter>
				</PromptInput>
			</div>
		</div>
	)
}
