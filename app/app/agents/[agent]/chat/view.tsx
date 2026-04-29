"use client"

import { createClient, type Transport } from "@connectrpc/connect"
import { useTransport } from "@connectrpc/connect-query"
import { ArrowLeft, Bot, ChevronDown, ChevronRight, Clock, Send, User, Wrench } from "lucide-react"
import Link from "next/link"
import { useEffect, useMemo, useRef, useState } from "react"
import { useRouteParams } from "@/lib/use-route-params"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Runtime, type ChatStreamEvent } from "@/lib/proto/v1/daemon_pb"
import { cn } from "@/lib/utils"

interface UIMessage {
	id: string
	role: "user" | "assistant"
	content: string
	timestamp: Date
	steps: ChatStreamEvent[]
}

function StepEvent({ step }: { step: ChatStreamEvent }) {
	const [isOpen, setIsOpen] = useState(false)

	return (
		<Collapsible onOpenChange={setIsOpen} open={isOpen}>
			<CollapsibleTrigger asChild>
				<button
					className="flex w-full items-center gap-2 rounded-md bg-muted/50 px-3 py-2 text-left text-sm transition-colors hover:bg-muted"
					type="button">
					{isOpen ? (
						<ChevronDown className="h-4 w-4 shrink-0" />
					) : (
						<ChevronRight className="h-4 w-4 shrink-0" />
					)}
					<Wrench className="h-4 w-4 shrink-0 text-primary" />
					<span className="flex-1 font-medium">{step.tool || step.type}</span>
					<span className="flex items-center gap-1 text-muted-foreground text-xs">
						<Clock className="h-3 w-3" />
						step {step.step}
					</span>
				</button>
			</CollapsibleTrigger>
			<CollapsibleContent>
				<div className="mt-2 rounded-md border bg-background p-3">
					<pre className="max-h-32 overflow-auto whitespace-pre-wrap text-xs">{step.content}</pre>
				</div>
			</CollapsibleContent>
		</Collapsible>
	)
}

function Message({ message }: { message: UIMessage }) {
	const isUser = message.role === "user"

	return (
		<div className={cn("flex gap-3", isUser ? "flex-row-reverse" : "flex-row")}>
			<div
				className={cn(
					"flex h-8 w-8 shrink-0 items-center justify-center rounded-full",
					isUser ? "bg-primary text-primary-foreground" : "bg-muted",
				)}>
				{isUser ? <User className="h-4 w-4" /> : <Bot className="h-4 w-4" />}
			</div>
			<div className="max-w-[80%] space-y-2">
				{message.steps.length > 0 && (
					<div className="space-y-1">
						{message.steps.map((step) => (
							<StepEvent key={`${step.step}-${step.type}`} step={step} />
						))}
					</div>
				)}
				<div
					className={cn(
						"rounded-lg px-4 py-2",
						isUser ? "bg-primary text-primary-foreground" : "bg-muted",
					)}>
					<div className="prose prose-sm dark:prose-invert max-w-none whitespace-pre-wrap">
						{message.content}
					</div>
				</div>
				<p className="text-muted-foreground text-xs">{message.timestamp.toLocaleTimeString()}</p>
			</div>
		</div>
	)
}

export default function ChatPage() {
	const params = useRouteParams<{ agent: string }>("/agents/:agent/chat")
	const agentName = params.agent ?? ""
	const transport = useTransport() as Transport
	// Stable client over the page's lifetime — re-creating it would
	// abort any in-flight stream when React re-renders.
	const client = useMemo(() => createClient(Runtime, transport), [transport])

	const [messages, setMessages] = useState<UIMessage[]>([])
	const [input, setInput] = useState("")
	const [isStreaming, setIsStreaming] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const scrollRef = useRef<HTMLDivElement>(null)
	// Each chat invocation gets a fresh ephemeral session unless a
	// reusable id is plumbed in later. Keeps the demo independent.
	const sessionId = useMemo(() => `web:chat:${crypto.randomUUID()}`, [])

	useEffect(() => {
		if (scrollRef.current) {
			scrollRef.current.scrollTop = scrollRef.current.scrollHeight
		}
	}, [messages])

	const handleSend = async () => {
		const prompt = input.trim()
		if (!prompt || isStreaming || agentName === "") {
			return
		}

		const userMessage: UIMessage = {
			id: `msg-${Date.now()}-user`,
			role: "user",
			content: prompt,
			timestamp: new Date(),
			steps: [],
		}

		const assistantMessage: UIMessage = {
			id: `msg-${Date.now()}-assistant`,
			role: "assistant",
			content: "",
			timestamp: new Date(),
			steps: [],
		}

		setMessages((prev) => [...prev, userMessage, assistantMessage])
		setInput("")
		setIsStreaming(true)
		setError(null)

		try {
			const stream = client.chatStreamWithAgent({
				ref: agentName,
				sessionId,
				prompt,
			})

			for await (const event of stream) {
				setMessages((prev) => {
					const next = [...prev]
					const last = next[next.length - 1]
					if (!last || last.role !== "assistant") {
						return next
					}

					if (event.type === "text.delta") {
						last.content = last.content + event.content
					} else {
						last.steps = [...last.steps, event]
					}

					next[next.length - 1] = { ...last }
					return next
				})
			}
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err))
		} finally {
			setIsStreaming(false)
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
						<h1 className="font-semibold">{agentName}</h1>
						<p className="font-mono text-muted-foreground text-xs">session {sessionId.slice(-8)}</p>
					</div>
				</div>
			</div>

			<ScrollArea className="flex-1 px-4" ref={scrollRef}>
				<div className="space-y-6 py-4">
					{messages.length === 0 && (
						<p className="text-center text-muted-foreground text-sm">
							Send a message to start the conversation.
						</p>
					)}
					{messages.map((message) => (
						<Message key={message.id} message={message} />
					))}
					{isStreaming && (
						<div className="flex gap-3">
							<div className="flex h-8 w-8 items-center justify-center rounded-full bg-muted">
								<Bot className="h-4 w-4" />
							</div>
							<div className="flex items-center gap-1 rounded-lg bg-muted px-4 py-2">
								<span
									className="h-2 w-2 animate-bounce rounded-full bg-foreground/50"
									style={{ animationDelay: "0ms" }}
								/>
								<span
									className="h-2 w-2 animate-bounce rounded-full bg-foreground/50"
									style={{ animationDelay: "150ms" }}
								/>
								<span
									className="h-2 w-2 animate-bounce rounded-full bg-foreground/50"
									style={{ animationDelay: "300ms" }}
								/>
							</div>
						</div>
					)}
					{error && (
						<p className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-destructive text-sm">
							{error}
						</p>
					)}
				</div>
			</ScrollArea>

			<div className="border-t p-4">
				<form
					className="flex gap-2"
					onSubmit={(e) => {
						e.preventDefault()
						handleSend()
					}}>
					<Input
						className="flex-1"
						disabled={isStreaming}
						onChange={(e) => setInput(e.target.value)}
						placeholder="Type a message..."
						value={input}
					/>
					<Button disabled={!input.trim() || isStreaming || agentName === ""} type="submit">
						<Send className="h-4 w-4" />
						<span className="sr-only">Send</span>
					</Button>
				</form>
			</div>
		</div>
	)
}
