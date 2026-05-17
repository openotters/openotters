"use client"

import { createClient } from "@connectrpc/connect"
import { Pause, Play, X } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select"
import {
	Sheet,
	SheetContent,
	SheetDescription,
	SheetHeader,
	SheetTitle,
} from "@/components/ui/sheet"
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table"
import type { RPCCallEvent } from "@/lib/proto/v1/daemon_pb"
import { Runtime } from "@/lib/proto/v1/daemon_pb"
import { transport } from "@/lib/transport"

// BufferedEvent wraps each captured RPCCallEvent with a stable
// client-assigned id. Reusing the proto's timestampUnixMs as a React
// key resets row identity when two events land in the same
// millisecond (the table re-keys every row, unmounting and
// remounting), and that's what was throwing scroll position back to
// the top on every push. A monotonic counter sidesteps the problem
// without relying on server-side IDs.
interface BufferedEvent {
	id: number
	event: RPCCallEvent
}

// Maximum events kept client-side. The server replays up to 200 on
// (re)subscribe, then streams live. The buffer caps memory growth on
// a long-open tab — older rows fall off the bottom.
const CLIENT_BUFFER_SIZE = 1000

interface Filters {
	servicePrefix: string
	procedurePrefix: string
	callerKind: string // "" / "operator" / "agent" / "anonymous"
	status: string // "" / "ok" / "error"
	minDurationMs: string
}

const emptyFilters: Filters = {
	servicePrefix: "",
	procedurePrefix: "",
	callerKind: "",
	status: "",
	minDurationMs: "",
}

// RpcMonitor subscribes to StreamRPCCalls and renders a live table.
// The component restarts the subscription whenever a filter changes
// — server-side filtering keeps the wire small even when the
// operator only cares about a narrow slice of traffic.
export function RpcMonitor() {
	const [filters, setFilters] = useState<Filters>(emptyFilters)
	const [paused, setPaused] = useState(false)
	const [events, setEvents] = useState<BufferedEvent[]>([])
	const [error, setError] = useState<string | null>(null)
	const [selected, setSelected] = useState<BufferedEvent | null>(null)

	// Ref so the streaming loop can read the pause flag without
	// re-subscribing every time the user clicks.
	const pausedRef = useRef(paused)
	pausedRef.current = paused

	// Monotonic counter for stable React keys. Persists across
	// subscription restarts so a "Clear" + new burst doesn't collide
	// with old ids the React reconciler is still cleaning up.
	const idRef = useRef(0)

	// Stable filter signature so the subscription only restarts on
	// real changes — typing into a free-text input doesn't fire on
	// every keystroke (we debounce below).
	const filtersKey = JSON.stringify(filters)

	useEffect(() => {
		const client = createClient(Runtime, transport)
		const controller = new AbortController()

		const minDurationUs =
			filters.minDurationMs.trim() === ""
				? 0n
				: BigInt(Math.max(0, Math.floor(Number(filters.minDurationMs) * 1000)))

		// Reset the buffer on subscription restart — the server's
		// replay fills it with the most recent matching events; we
		// shouldn't keep stale rows that may not match the new
		// filter.
		setEvents([])
		setError(null)

		;(async () => {
			try {
				for await (const ev of client.streamRPCCalls(
					{
						servicePrefix: filters.servicePrefix.trim(),
						procedurePrefix: filters.procedurePrefix.trim(),
						callerKind: filters.callerKind,
						agentId: "",
						status: filters.status,
						minDurationUs,
						replayRecent: 200,
					},
					{ signal: controller.signal },
				)) {
					if (pausedRef.current) continue
					const id = idRef.current++
					setEvents((prev) => {
						const next = [{ id, event: ev }, ...prev]
						return next.length > CLIENT_BUFFER_SIZE
							? next.slice(0, CLIENT_BUFFER_SIZE)
							: next
					})
				}
			} catch (e) {
				if (!controller.signal.aborted) {
					setError(e instanceof Error ? e.message : String(e))
				}
			}
		})()

		return () => controller.abort()
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [filtersKey])

	const ratePerSec = useRate(events)

	return (
		<Card>
			<CardHeader>
				<div className="flex items-center justify-between gap-4">
					<div>
						<CardTitle>RPC monitor</CardTitle>
						<CardDescription>
							Live tail of every Connect call hitting the daemon — including auth-failed
							and future RPCs (no per-handler wiring). Filters apply server-side.
						</CardDescription>
					</div>
					<div className="flex items-center gap-2">
						<Badge variant="secondary">{ratePerSec.toFixed(1)} /s</Badge>
						<Badge variant="outline">{events.length} buffered</Badge>
						<Button
							disabled={events.length === 0}
							onClick={() => setEvents([])}
							size="sm"
							variant="outline"
						>
							<X className="mr-1 h-3 w-3" />
							Clear
						</Button>
						<Button
							onClick={() => setPaused((p) => !p)}
							size="sm"
							variant={paused ? "default" : "outline"}
						>
							{paused ? (
								<>
									<Play className="mr-1 h-3 w-3" />
									Resume
								</>
							) : (
								<>
									<Pause className="mr-1 h-3 w-3" />
									Pause
								</>
							)}
						</Button>
					</div>
				</div>
			</CardHeader>
			<CardContent className="space-y-4">
				<FilterBar filters={filters} onChange={setFilters} />
				{error && <p className="text-sm text-destructive">Stream error: {error}</p>}
				<ScrollArea className="h-[65vh] rounded-md border">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead className="w-[110px]">Time</TableHead>
								<TableHead>RPC</TableHead>
								<TableHead className="w-[160px]">Caller</TableHead>
								<TableHead className="w-[120px]">Status</TableHead>
								<TableHead className="w-[100px] text-right">Duration</TableHead>
								<TableHead className="w-[100px] text-right">Size</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{events.length === 0 ? (
								<TableRow>
									<TableCell className="text-center text-muted-foreground" colSpan={6}>
										Waiting for traffic…
									</TableCell>
								</TableRow>
							) : (
								events.map((be) => (
									<RpcRow
										event={be.event}
										key={be.id}
										onClick={() => setSelected(be)}
										selected={selected?.id === be.id}
									/>
								))
							)}
						</TableBody>
					</Table>
				</ScrollArea>
			</CardContent>

			<Sheet onOpenChange={(open) => !open && setSelected(null)} open={selected !== null}>
				<SheetContent className="w-[480px] sm:max-w-[480px]">
					{selected && <EventDetails event={selected.event} />}
				</SheetContent>
			</Sheet>
		</Card>
	)
}

// EventDetails renders every captured field for one RPC call as a
// labelled list. The recorder never captures payloads or headers —
// what we show here is the full set of fields the middleware logs;
// there's no hidden detail we're holding back.
function EventDetails({ event }: { event: RPCCallEvent }) {
	const ts = new Date(Number(event.timestampUnixMs))
	const durationMs = Number(event.durationUs) / 1000
	const totalBytes = Number(event.bytesIn) + Number(event.bytesOut)

	const rows: Array<{ label: string; value: React.ReactNode }> = [
		{ label: "Time", value: <span className="font-mono">{ts.toISOString()}</span> },
		{
			label: "Procedure",
			value: (
				<span className="break-all font-mono text-xs">
					{event.service}.{event.procedure}
				</span>
			),
		},
		{
			label: "Caller",
			value: <span className="font-mono">{event.caller || "anonymous"}</span>,
		},
		{
			label: "Status",
			value: (
				<Badge variant={event.status === "ok" ? "secondary" : "destructive"}>
					{event.status}
				</Badge>
			),
		},
		{ label: "Stream type", value: event.streamType },
		{ label: "Duration", value: `${durationMs.toFixed(3)} ms` },
		{ label: "Bytes in", value: event.bytesIn.toString() },
		{ label: "Bytes out", value: event.bytesOut.toString() },
		{ label: "Total bytes", value: formatBytes(totalBytes) },
	]

	if (event.errMessage) {
		rows.push({
			label: "Error",
			value: (
				<pre className="whitespace-pre-wrap break-words text-destructive text-xs">
					{event.errMessage}
				</pre>
			),
		})
	}

	return (
		<>
			<SheetHeader>
				<SheetTitle>RPC details</SheetTitle>
				<SheetDescription>
					Metadata captured by the daemon's HTTP middleware. Payloads and headers are never
					recorded.
				</SheetDescription>
			</SheetHeader>
			<dl className="mt-4 space-y-3">
				{rows.map((row) => (
					<div className="grid grid-cols-[120px_1fr] gap-2" key={row.label}>
						<dt className="text-muted-foreground text-sm">{row.label}</dt>
						<dd className="text-sm">{row.value}</dd>
					</div>
				))}
			</dl>
		</>
	)
}

function FilterBar({
	filters,
	onChange,
}: {
	filters: Filters
	onChange: (next: Filters) => void
}) {
	const set = (k: keyof Filters) => (v: string) => onChange({ ...filters, [k]: v })

	return (
		<div className="grid gap-3 md:grid-cols-[1fr_1fr_180px_160px_140px]">
			<div>
				<Label htmlFor="rpc-service">Service</Label>
				<Input
					id="rpc-service"
					onChange={(e) => set("servicePrefix")(e.target.value)}
					placeholder="Runtime"
					value={filters.servicePrefix}
				/>
			</div>
			<div>
				<Label htmlFor="rpc-procedure">Procedure prefix</Label>
				<Input
					id="rpc-procedure"
					onChange={(e) => set("procedurePrefix")(e.target.value)}
					placeholder="Save"
					value={filters.procedurePrefix}
				/>
			</div>
			<div>
				<Label htmlFor="rpc-caller">Caller</Label>
				<Select onValueChange={set("callerKind")} value={filters.callerKind || "any"}>
					<SelectTrigger id="rpc-caller">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value="any">Any</SelectItem>
						<SelectItem value="operator">Operator</SelectItem>
						<SelectItem value="agent">Agent</SelectItem>
						<SelectItem value="anonymous">Anonymous (auth failed)</SelectItem>
					</SelectContent>
				</Select>
			</div>
			<div>
				<Label htmlFor="rpc-status">Status</Label>
				<Select onValueChange={set("status")} value={filters.status || "any"}>
					<SelectTrigger id="rpc-status">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value="any">Any</SelectItem>
						<SelectItem value="ok">OK</SelectItem>
						<SelectItem value="error">Error</SelectItem>
					</SelectContent>
				</Select>
			</div>
			<div>
				<Label htmlFor="rpc-duration">Min duration (ms)</Label>
				<Input
					id="rpc-duration"
					inputMode="numeric"
					onChange={(e) => set("minDurationMs")(e.target.value)}
					placeholder="0"
					value={filters.minDurationMs}
				/>
			</div>
		</div>
	)
}

function RpcRow({
	event,
	onClick,
	selected,
}: {
	event: RPCCallEvent
	onClick: () => void
	selected: boolean
}) {
	const t = new Date(Number(event.timestampUnixMs)).toLocaleTimeString(undefined, {
		hour12: false,
	})
	const duration =
		event.durationUs < 1000n
			? `${event.durationUs.toString()} µs`
			: `${(Number(event.durationUs) / 1000).toFixed(1)} ms`
	const size = formatBytes(Number(event.bytesIn) + Number(event.bytesOut))
	const caller = event.caller || "anonymous"
	const isErr = event.status !== "ok"

	return (
		<TableRow
			className={`cursor-pointer ${isErr ? "bg-destructive/5" : ""} ${
				selected ? "bg-muted" : ""
			}`}
			onClick={onClick}
		>
			<TableCell className="font-mono text-xs">{t}</TableCell>
			<TableCell className="font-mono text-xs">
				<span className="text-muted-foreground">{event.service}.</span>
				{event.procedure}
				{event.streamType === "server-stream" && (
					<Badge className="ml-2 px-1 py-0 text-[10px]" variant="outline">
						stream
					</Badge>
				)}
			</TableCell>
			<TableCell className="font-mono text-xs">{caller}</TableCell>
			<TableCell>
				<Badge variant={isErr ? "destructive" : "secondary"}>{event.status}</Badge>
				{event.errMessage && (
					<span className="ml-2 text-xs text-muted-foreground">{event.errMessage}</span>
				)}
			</TableCell>
			<TableCell className="text-right font-mono text-xs">{duration}</TableCell>
			<TableCell className="text-right font-mono text-xs">{size}</TableCell>
		</TableRow>
	)
}

function formatBytes(n: number): string {
	if (n < 1024) return `${n} B`
	if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
	return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

// useRate returns an estimated events-per-second rate based on the
// last K events' timestamps. Live UI signal; not load-bearing.
function useRate(events: BufferedEvent[]): number {
	return useMemo(() => {
		if (events.length < 2) return 0
		const recent = events.slice(0, Math.min(50, events.length))
		const newest = Number(recent[0].event.timestampUnixMs)
		const oldest = Number(recent[recent.length - 1].event.timestampUnixMs)
		const dtMs = newest - oldest
		if (dtMs <= 0) return 0
		return (recent.length / dtMs) * 1000
	}, [events])
}
