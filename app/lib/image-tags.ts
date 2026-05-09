// Image-tag grouping helpers shared between the /images and /bins
// listings + their detail views.
//
// The daemon's ListImages RPC returns one entry per ref/tag pair,
// so a single image with two tags (e.g. `meteo:v1` + `meteo:latest`)
// shows up twice. The listings group by digest so each image is
// rendered once; the detail views surface every tag that points at
// the same digest.

export interface TaggedImage {
	ref: string
	digest: string
	artifactType: string
	size: bigint
	createdAt: bigint
	description: string
	source: string
}

export interface ImageGroup<T extends TaggedImage> {
	// primary is the canonical ref for the listing card title.
	primary: T
	// refs is every ref pointing at the same digest, sorted.
	refs: string[]
	// all carries the original entries so callers can pull metadata
	// (size, createdAt, etc.) from any tag of the group when the
	// daemon happens to return slightly different values per tag
	// (e.g. mismatched createdAt).
	all: T[]
}

// pickPrimaryRef ranks refs so the listing card shows a
// human-friendly choice without relying on insertion order:
//
//   1. ref ending in `:latest` wins
//   2. shortest ref (typically the bare repo name)
//   3. lexicographic tiebreak
//
// Stable across renders since the underlying refs slice is sorted
// before ranking.
export function pickPrimaryRef(refs: string[]): string {
	if (refs.length === 0) return ""
	if (refs.length === 1) return refs[0]

	const latest = refs.find((r) => r.endsWith(":latest"))
	if (latest) return latest

	return [...refs].sort((a, b) => {
		if (a.length !== b.length) return a.length - b.length
		return a.localeCompare(b)
	})[0]
}

export function groupImagesByDigest<T extends TaggedImage>(images: T[]): ImageGroup<T>[] {
	const byDigest = new Map<string, T[]>()
	for (const img of images) {
		const list = byDigest.get(img.digest) ?? []
		list.push(img)
		byDigest.set(img.digest, list)
	}

	const groups: ImageGroup<T>[] = []
	for (const [, all] of byDigest) {
		const refs = [...new Set(all.map((i) => i.ref))].sort((a, b) => a.localeCompare(b))
		const primaryRef = pickPrimaryRef(refs)
		const primary = all.find((i) => i.ref === primaryRef) ?? all[0]
		groups.push({ primary, refs, all })
	}

	return groups.sort((a, b) => a.primary.ref.localeCompare(b.primary.ref))
}

// refsForDigest returns every ref in the list that shares a digest
// with the given target ref. Used by the detail views to render the
// "tags pointing at this image" card.
export function refsForDigest<T extends TaggedImage>(
	images: T[],
	digest: string,
): string[] {
	const refs = new Set<string>()
	for (const img of images) {
		if (img.digest === digest) refs.add(img.ref)
	}
	return [...refs].sort((a, b) => a.localeCompare(b))
}
