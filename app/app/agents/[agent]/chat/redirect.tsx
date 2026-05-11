"use client"

import { useRouter } from "next/navigation"
import { useEffect } from "react"
import { useRouteParams } from "@/lib/use-route-params"

// Mints a fresh session id on first visit and rewrites the URL to
// /agents/<name>/chat/<session> so a reload keeps the conversation.
// The session id is the key the daemon uses for chat-stream
// continuity; embedding it in the URL makes the back button and
// bookmarks resume the same thread.
//
// The id format mirrors the CLI's `cli:chat:<uuid>` (see
// cmd/otters/commands/chat.go) — same shape, different surface
// prefix. Operators filtering /jobs by `io.openotters.session-id`
// (which job_submit auto-stamps) can therefore tell at a glance
// whether a session originated from the web (`gui:chat:…`) or the
// CLI (`cli:chat:…`).
export default function ChatRedirect() {
	const params = useRouteParams<{ agent: string }>("/agents/:agent/chat")
	const router = useRouter()

	useEffect(() => {
		const agent = params.agent
		if (!agent) return
		const id = `gui:chat:${crypto.randomUUID()}`
		// encodeURIComponent because the colons in "gui:chat:…"
		// are reserved characters in URI paths — they're tolerated
		// by every browser we care about, but `useRouteParams`
		// already does `decodeURIComponent` on each segment so
		// percent-encoding here makes the round-trip explicit.
		router.replace(
			`/agents/${encodeURIComponent(agent)}/chat/${encodeURIComponent(id)}`,
		)
	}, [params.agent, router])

	return <p className="text-muted-foreground text-sm">Starting session…</p>
}
