"use client"

import { useRouter } from "next/navigation"
import { useEffect } from "react"
import { useRouteParams } from "@/lib/use-route-params"

// Mints a fresh session id on first visit and rewrites the URL to
// /agents/<name>/chat/<session> so a reload keeps the conversation.
// The session id is the key the daemon uses for chat-stream
// continuity; embedding it in the URL makes the back button and
// bookmarks resume the same thread.
export default function ChatRedirect() {
	const params = useRouteParams<{ agent: string }>("/agents/:agent/chat")
	const router = useRouter()

	useEffect(() => {
		const agent = params.agent
		if (!agent) return
		const id = crypto.randomUUID().slice(0, 12)
		router.replace(`/agents/${encodeURIComponent(agent)}/chat/${id}`)
	}, [params.agent, router])

	return <p className="text-muted-foreground text-sm">Starting session…</p>
}
