"use client"

import { useRef } from "react"

// useStableSort orders items by the supplied comparator, with a
// stable tiebreaker on the order each item was first seen — so two
// items that compare equal don't swap places on a refetch.
//
// `getKey` must return a stable identifier per item (e.g. agent.id,
// image.digest, provider.name). It's used both as the React key for
// the rendered list and as the lookup key for the first-seen index.
export function useStableSort<T>(
	items: readonly T[],
	getKey: (t: T) => string,
	compare: (a: T, b: T) => number,
): T[] {
	const seenRef = useRef<Map<string, number>>(new Map())
	const counterRef = useRef(0)

	const seen = seenRef.current

	for (const item of items) {
		const k = getKey(item)
		if (!seen.has(k)) {
			seen.set(k, counterRef.current++)
		}
	}

	const idxOf = (t: T) => seen.get(getKey(t)) ?? Number.MAX_SAFE_INTEGER

	const next = items.slice()

	next.sort((a, b) => {
		const c = compare(a, b)
		if (c !== 0) return c
		return idxOf(a) - idxOf(b)
	})

	return next
}
