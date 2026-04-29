import { cn } from "@/lib/utils"

// Status strings come from the daemon over the wire (`AgentInfo.status`,
// provider config, etc.) so we accept any string — the daemon is the
// source of truth for what status names exist. Unknown statuses fall
// back to a neutral muted style.
type StatusKey =
	| "provisioning"
	| "running"
	| "degraded"
	| "stopped"
	| "pending"
	| "completed"
	| "failed"
	| "active"
	| "error"
	| "connected"
	| "disconnected"
	| "scaling"
	| "suspended"
	| "init_error"
	| "pull_error"
	| "model_error"
	| "removing"
	| "removed"
	| "created"

interface StatusBadgeProps {
	status: string
	className?: string
}

interface StatusVisual {
	label: string
	className: string
	dotClassName: string
}

const statusConfig: Record<StatusKey, StatusVisual> = {
	running: { label: "Running", className: "bg-emerald-500/10 text-emerald-500", dotClassName: "bg-emerald-500" },
	active: { label: "Active", className: "bg-emerald-500/10 text-emerald-500", dotClassName: "bg-emerald-500" },
	connected: { label: "Connected", className: "bg-emerald-500/10 text-emerald-500", dotClassName: "bg-emerald-500" },
	completed: { label: "Completed", className: "bg-emerald-500/10 text-emerald-500", dotClassName: "bg-emerald-500" },
	created: { label: "Created", className: "bg-emerald-500/10 text-emerald-500", dotClassName: "bg-emerald-500" },
	provisioning: {
		label: "Provisioning",
		className: "bg-amber-500/10 text-amber-500",
		dotClassName: "bg-amber-500 animate-pulse",
	},
	scaling: {
		label: "Scaling",
		className: "bg-amber-500/10 text-amber-500",
		dotClassName: "bg-amber-500 animate-pulse",
	},
	pending: { label: "Pending", className: "bg-blue-500/10 text-blue-500", dotClassName: "bg-blue-500" },
	degraded: { label: "Degraded", className: "bg-orange-500/10 text-orange-500", dotClassName: "bg-orange-500" },
	error: { label: "Error", className: "bg-red-500/10 text-red-500", dotClassName: "bg-red-500" },
	failed: { label: "Failed", className: "bg-red-500/10 text-red-500", dotClassName: "bg-red-500" },
	init_error: { label: "Init error", className: "bg-red-500/10 text-red-500", dotClassName: "bg-red-500" },
	pull_error: { label: "Pull error", className: "bg-red-500/10 text-red-500", dotClassName: "bg-red-500" },
	model_error: { label: "Model error", className: "bg-red-500/10 text-red-500", dotClassName: "bg-red-500" },
	stopped: { label: "Stopped", className: "bg-muted text-muted-foreground", dotClassName: "bg-muted-foreground" },
	removing: { label: "Removing", className: "bg-muted text-muted-foreground", dotClassName: "bg-muted-foreground" },
	removed: { label: "Removed", className: "bg-muted text-muted-foreground", dotClassName: "bg-muted-foreground" },
	disconnected: {
		label: "Disconnected",
		className: "bg-muted text-muted-foreground",
		dotClassName: "bg-muted-foreground",
	},
	suspended: { label: "Suspended", className: "bg-muted text-muted-foreground", dotClassName: "bg-muted-foreground" },
}

const fallbackVisual: StatusVisual = {
	label: "Unknown",
	className: "bg-muted text-muted-foreground",
	dotClassName: "bg-muted-foreground",
}

function visualFor(status: string): StatusVisual {
	const config = statusConfig[status as StatusKey]
	if (config) {
		return config
	}

	// Unknown status — render the raw daemon string with a neutral
	// style instead of "Unknown" so debugging is easier.
	return { ...fallbackVisual, label: status || fallbackVisual.label }
}

export function StatusBadge({ status, className }: StatusBadgeProps) {
	const visual = visualFor(status)

	return (
		<span
			className={cn(
				"inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 font-medium text-xs",
				visual.className,
				className,
			)}>
			<span className={cn("h-1.5 w-1.5 rounded-full", visual.dotClassName)} />
			{visual.label}
		</span>
	)
}
