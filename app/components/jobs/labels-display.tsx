import { Badge } from "@/components/ui/badge"

interface LabelsDisplayProps {
	labels: { [key: string]: string }
	// compact mode caps to 2 visible chips + "+N" overflow chip,
	// shrinking the chip text for table cells. Non-compact lists
	// every label, full text — used in the job detail panel.
	compact?: boolean
}

// LabelsDisplay renders a key=value chip list. Sorted by key for
// deterministic ordering — same labels always render in the same
// position, so a row glance is reliable.
export function LabelsDisplay({ labels, compact = false }: LabelsDisplayProps) {
	const entries = Object.entries(labels).sort(([a], [b]) => a.localeCompare(b))

	if (entries.length === 0) {
		return <span className="text-muted-foreground text-xs">-</span>
	}

	if (compact) {
		const visible = entries.slice(0, 2)
		const hidden = entries.length - visible.length
		return (
			<div className="flex flex-wrap items-center gap-1">
				{visible.map(([k, v]) => (
					<LabelChip compact key={k} k={k} v={v} />
				))}
				{hidden > 0 && (
					<Badge className="text-xs" variant="outline">
						+{hidden}
					</Badge>
				)}
			</div>
		)
	}

	return (
		<div className="flex flex-wrap gap-1.5">
			{entries.map(([k, v]) => (
				<LabelChip k={k} key={k} v={v} />
			))}
		</div>
	)
}

function LabelChip({ k, v, compact }: { k: string; v: string; compact?: boolean }) {
	return (
		<Badge
			className={compact ? "max-w-[160px] truncate font-mono text-[10px]" : "font-mono text-xs"}
			title={`${k}=${v}`}
			variant="outline">
			<span className="text-muted-foreground">{k}</span>
			<span className="mx-0.5 text-muted-foreground">=</span>
			<span>{v}</span>
		</Badge>
	)
}
