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

// NameGroup represents one repository (e.g.
// `ghcr.io/openotters/runtime`) collapsed across every tag the daemon
// returned. The listing pages render one card per NameGroup so the
// operator sees images by what they ARE (a runtime, a curl) instead
// of one row per tag.
export interface NameGroup<T extends TaggedImage> {
	// name is the repository path with no tag, e.g.
	// `ghcr.io/openotters/runtime`.
	name: string
	// digests is every distinct digest seen under this name, each with
	// its tag set. Sorted by createdAt desc (newest digest first).
	digests: ImageGroup<T>[]
	// primary is the representative entry used for the listing card —
	// the `:latest` tag's row if present, else the newest digest's
	// primary, falling back to digests[0].primary.
	primary: T
}

// groupImagesByName collapses every input row into one NameGroup per
// repository, with each digest's tag set preserved as an inner
// ImageGroup. Used by the /bins and /images list pages to surface one
// card per image instead of one per tag.
export function groupImagesByName<T extends TaggedImage>(images: T[]): NameGroup<T>[] {
	const byName = new Map<string, T[]>()
	for (const img of images) {
		const n = parseRefName(img.ref)
		const list = byName.get(n) ?? []
		list.push(img)
		byName.set(n, list)
	}

	const groups: NameGroup<T>[] = []
	for (const [name, entries] of byName) {
		const digests = groupImagesByDigest(entries).sort((a, b) => {
			if (a.primary.createdAt !== b.primary.createdAt) {
				return Number(b.primary.createdAt - a.primary.createdAt)
			}
			return a.primary.ref.localeCompare(b.primary.ref)
		})

		const latest = entries.find((i) => i.ref.endsWith(":latest"))
		const primary = latest ?? digests[0]?.primary ?? entries[0]

		groups.push({ name, digests, primary })
	}

	return groups.sort((a, b) => a.name.localeCompare(b.name))
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

// parseRefName returns the repository portion of an OCI ref, stripping
// the trailing tag if present. A colon is a tag separator only when it
// appears after the last slash — otherwise it's a registry port
// (e.g. `localhost:5000/foo`).
export function parseRefName(ref: string): string {
	const lastSlash = ref.lastIndexOf("/")
	const lastColon = ref.lastIndexOf(":")
	if (lastColon > lastSlash) return ref.substring(0, lastColon)
	return ref
}

export function parseRefTag(ref: string): string {
	const name = parseRefName(ref)
	if (name === ref) return ""
	return ref.substring(name.length + 1)
}

// versionsForRef returns every entry that shares the repository name
// of the target ref — i.e. all versions/tags of the same image.
// Sorted with `:latest` first, then most recently built, then by ref.
export function versionsForRef<T extends TaggedImage>(
	images: T[],
	targetRef: string,
): T[] {
	const name = parseRefName(targetRef)
	const matches = images.filter((i) => parseRefName(i.ref) === name)
	return matches.sort((a, b) => {
		const aLatest = a.ref.endsWith(":latest")
		const bLatest = b.ref.endsWith(":latest")
		if (aLatest !== bLatest) return aLatest ? -1 : 1
		if (a.createdAt !== b.createdAt) {
			return Number(b.createdAt - a.createdAt)
		}
		return a.ref.localeCompare(b.ref)
	})
}
