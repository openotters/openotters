"use client"

import { useEffect, useState } from "react"

// useRouteParams reads dynamic path segments from `window.location` at
// runtime. We need this because Next's `output: "export"` bakes
// `useParams()` to whatever was returned by `generateStaticParams()`
// at build time. Our dynamic routes export with a single `_`
// placeholder, so `useParams()` would return `_` regardless of the
// real URL — the daemon serves the right placeholder bundle (see
// `internal/webui/webui.go`), but the React layer still needs to know
// the actual segment.
//
// Pattern syntax: ":name" segments are captured; literal segments
// must match. Returns an empty object on the first render before the
// effect runs (no `window` during SSR — Next renders the component
// once on the server during static export); pages should treat the
// empty state as "loading".
export function useRouteParams<T extends Record<string, string>>(
	pattern: string,
): Partial<T> {
	const [params, setParams] = useState<Partial<T>>({})

	useEffect(() => {
		const patternSegs = pattern.split("/").filter(Boolean)
		const pathSegs = window.location.pathname.split("/").filter(Boolean)

		if (patternSegs.length !== pathSegs.length) {
			return
		}

		const out: Record<string, string> = {}

		for (let i = 0; i < patternSegs.length; i++) {
			const p = patternSegs[i]
			const v = pathSegs[i]

			if (p.startsWith(":")) {
				out[p.slice(1)] = decodeURIComponent(v)
				continue
			}

			if (p !== v) {
				return
			}
		}

		setParams(out as Partial<T>)
	}, [pattern])

	return params
}
