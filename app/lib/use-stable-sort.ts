"use client"

import { useRef } from "react"

export type SortDir = "asc" | "desc"

export interface SortSpec<T> {
	// null → "default": preserve the order each item was first seen by
	// the hook. Append new items at the end on subsequent renders, so
	// a refetch that returns the same N items in a different order
	// still renders them in their original slots.
	compare: ((a: T, b: T) => number) | null
}

// useStableSort orders items deterministically across refetches.
//
// `getKey` must return a stable identifier per item (e.g. agent.id,
// image.digest, provider.name). It's used both as the React key for
// the rendered list and as the lookup key for the first-seen index.
//
// When `sort.compare` is null, items are returned in the order they
// were first observed. New items take the next unused slot at the
// end. Items that disappear are forgotten only if they don't show up
// again later; a temporary network blip won't reorder them.
//
// When `sort.compare` is non-null, that comparator runs on a copy of
// the input. The first-seen ordering still serves as the stable
// secondary key (so equal-by-comparator items keep a consistent
// relative order across renders).
export function useStableSort<T>(
	items: readonly T[],
	getKey: (t: T) => string,
	sort: SortSpec<T>,
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

	const next = items.slice()

	const idxOf = (t: T) => seen.get(getKey(t)) ?? Number.MAX_SAFE_INTEGER

	if (sort.compare === null) {
		next.sort((a, b) => idxOf(a) - idxOf(b))
		return next
	}

	const compare = sort.compare

	next.sort((a, b) => {
		const c = compare(a, b)
		if (c !== 0) return c
		return idxOf(a) - idxOf(b)
	})

	return next
}
