import { cn } from "@/lib/utils"

// Status strings come from the daemon over the wire (`AgentInfo.status`,
// provider config, etc.) so we accept any string — the daemon is the
// source of truth for what status names exist. Unknown statuses fall
// back to a neutral muted style.
type StatusKey =
	// Agent lifecycle — see /docs/runtime#agent-lifecycle and
	// agentfile/executor/status.go.
	| "pulling"
	| "starting"
	| "ready"
	| "working"
	| "stopped"
	| "failed"
	| "removing"
	| "removed"
	// Generic / provider statuses still used elsewhere on the dash.
	| "running"
	| "provisioning"
	| "degraded"
	| "pending"
	| "completed"
	| "active"
	| "error"
	| "connected"
	| "disconnected"
	| "scaling"
	| "suspended"
	// Async job statuses — share the visual language with agent
	// statuses so a "running" badge means the same thing whether
	// it's an agent or a job.
	| "done"
	| "cancelled"
	| "orphaned"

interface StatusBadgeProps {
	status: string
	className?: string
	// failureReason surfaces under the badge on status="failed" so
	// the operator sees the cause (pull / init / model /
	// readiness_timeout / crashed). Falsy values render nothing
	// extra.
	failureReason?: string
}

interface StatusVisual {
	label: string
	className: string
	dotClassName: string
}

const statusConfig: Record<StatusKey, StatusVisual> = {
	// Agent lifecycle — transitional states pulse, terminal/healthy
	// states are flat. "Ready" green, "Working" blue (distinguishable
	// at a glance from idle Ready when scanning the agent list).
	pulling: {
		label: "Pulling",
		className: "bg-amber-500/10 text-amber-500",
		dotClassName: "bg-amber-500 animate-pulse",
	},
	starting: {
		label: "Starting",
		className: "bg-amber-500/10 text-amber-500",
		dotClassName: "bg-amber-500 animate-pulse",
	},
	ready: { label: "Ready", className: "bg-emerald-500/10 text-emerald-500", dotClassName: "bg-emerald-500" },
	working: { label: "Working", className: "bg-blue-500/10 text-blue-500", dotClassName: "bg-blue-500 animate-pulse" },
	stopped: { label: "Stopped", className: "bg-muted text-muted-foreground", dotClassName: "bg-muted-foreground" },
	failed: { label: "Failed", className: "bg-red-500/10 text-red-500", dotClassName: "bg-red-500" },
	removing: { label: "Removing", className: "bg-muted text-muted-foreground", dotClassName: "bg-muted-foreground" },
	removed: { label: "Removed", className: "bg-muted text-muted-foreground", dotClassName: "bg-muted-foreground" },

	// Generic / provider statuses retained for the rest of the dashboard.
	running: { label: "Running", className: "bg-emerald-500/10 text-emerald-500", dotClassName: "bg-emerald-500" },
	active: { label: "Active", className: "bg-emerald-500/10 text-emerald-500", dotClassName: "bg-emerald-500" },
	connected: { label: "Connected", className: "bg-emerald-500/10 text-emerald-500", dotClassName: "bg-emerald-500" },
	completed: { label: "Completed", className: "bg-emerald-500/10 text-emerald-500", dotClassName: "bg-emerald-500" },
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
	disconnected: {
		label: "Disconnected",
		className: "bg-muted text-muted-foreground",
		dotClassName: "bg-muted-foreground",
	},
	suspended: { label: "Suspended", className: "bg-muted text-muted-foreground", dotClassName: "bg-muted-foreground" },
	done: { label: "Done", className: "bg-emerald-500/10 text-emerald-500", dotClassName: "bg-emerald-500" },
	cancelled: { label: "Cancelled", className: "bg-muted text-muted-foreground", dotClassName: "bg-muted-foreground" },
	orphaned: { label: "Orphaned", className: "bg-orange-500/10 text-orange-500", dotClassName: "bg-orange-500" },
}

// Map raw failure_reason wire values to human labels for the
// tooltip / inline-subscript surface.
const failureReasonLabel: Record<string, string> = {
	pull: "Image pull failed",
	init: "Workspace init failed",
	model: "Model resolution failed",
	readiness_timeout: "Readiness probe timed out",
	crashed: "Runtime crashed",
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

export function StatusBadge({ status, className, failureReason }: StatusBadgeProps) {
	const visual = visualFor(status)
	const reasonLabel = failureReason ? (failureReasonLabel[failureReason] ?? failureReason) : ""
	const titleAttr = status === "failed" && reasonLabel ? reasonLabel : undefined

	return (
		<span
			className={cn(
				"inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 font-medium text-xs",
				visual.className,
				className,
			)}
			title={titleAttr}>
			<span className={cn("h-1.5 w-1.5 rounded-full", visual.dotClassName)} />
			{visual.label}
			{status === "failed" && reasonLabel && (
				<span className="opacity-70">· {reasonLabel}</span>
			)}
		</span>
	)
}
