import { useMutation } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { MessageSquare, MoreVertical, Bot, Settings, Terminal, Trash2 } from "lucide-react"
import Link from "next/link"
import { toast } from "sonner"
import { ConfirmDelete } from "@/components/confirm-delete"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import type { AgentInfo } from "@/lib/proto/v1/daemon_pb"
import { removeAgent } from "@/lib/proto/v1/daemon-Runtime_connectquery"

interface AgentCardProps {
	agent: AgentInfo
}

// Convert the proto's int64 createdAt (bigint, Unix-seconds) into a
// JS Date. Component-local since AgentInfo is the only consumer.
function createdAtDate(unixSec: bigint): Date {
	return new Date(Number(unixSec) * 1000)
}

export function AgentCard({ agent }: AgentCardProps) {
	const basePath = `/agents/${agent.name}`
	const tools = agent.tools
	const queryClient = useQueryClient()

	const remove = useMutation(removeAgent, {
		onSuccess: () => {
			queryClient.invalidateQueries({
				queryKey: ["openotters.daemon.v1.Runtime", "ListAgents"],
			})
			toast.success(`Removed agent ${agent.name}`)
		},
		onError: (err) => {
			toast.error(`Failed to remove ${agent.name}`, { description: err.message })
		},
	})

	return (
		<Card className="group relative transition-colors hover:bg-muted/50">
			<CardHeader className="flex flex-row items-start justify-between space-y-0 pb-2">
				<div className="flex items-start gap-3">
					<div className="flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10">
						<Bot className="h-5 w-5 text-primary" />
					</div>
					<div>
						<CardTitle className="text-base">
							<Link className="hover:underline" href={basePath}>
								{agent.name}
							</Link>
						</CardTitle>
						<CardDescription className="font-mono text-xs">{agent.model}</CardDescription>
					</div>
				</div>
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<Button className="h-8 w-8" size="icon" variant="ghost">
							<MoreVertical className="h-4 w-4" />
							<span className="sr-only">Open menu</span>
						</Button>
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end">
						<DropdownMenuItem asChild>
							<Link href={basePath}>
								<Settings className="mr-2 h-4 w-4" />
								Details
							</Link>
						</DropdownMenuItem>
						<DropdownMenuItem asChild>
							<Link href={`${basePath}/chat`}>
								<MessageSquare className="mr-2 h-4 w-4" />
								Chat
							</Link>
						</DropdownMenuItem>
						<DropdownMenuSeparator />
						<ConfirmDelete
							description={
								<>
									This stops and removes agent{" "}
									<code className="font-mono text-xs">{agent.name}</code>. The image stays
									in the registry; only this instance is deleted.
								</>
							}
							onConfirm={() => remove.mutate({ ref: agent.name })}
							pending={remove.isPending}
							title="Delete agent?"
							trigger={(open) => (
								<DropdownMenuItem
									className="text-destructive focus:text-destructive"
									disabled={remove.isPending}
									onSelect={(e) => {
										e.preventDefault()
										open()
									}}>
									<Trash2 className="mr-2 h-4 w-4" />
									Delete
								</DropdownMenuItem>
							)}
						/>
					</DropdownMenuContent>
				</DropdownMenu>
			</CardHeader>
			<CardContent className="space-y-3">
				<div className="flex items-center gap-2">
					<StatusBadge status={agent.status} />
					{agent.image && agent.image !== "scratch" && (
						<span className="max-w-[150px] truncate text-muted-foreground text-xs">
							from: {agent.image.split("/").pop()}
						</span>
					)}
				</div>
				<div className="flex flex-wrap gap-2">
					{tools.slice(0, 3).map((tool) => (
						<span
							className="inline-flex items-center gap-1 rounded-md bg-secondary px-2 py-0.5 font-mono text-secondary-foreground text-xs"
							key={tool.name}>
							<Terminal className="h-3 w-3" />
							{tool.name}
						</span>
					))}
					{tools.length > 3 && (
						<span className="inline-flex items-center rounded-md bg-secondary px-2 py-0.5 text-secondary-foreground text-xs">
							+{tools.length - 3} more
						</span>
					)}
					{tools.length === 0 && <span className="text-muted-foreground text-xs">No bins configured</span>}
				</div>
				<div className="flex items-center justify-between pt-2">
					<Button asChild size="sm" variant="outline">
						<Link href={`${basePath}/chat`}>
							<MessageSquare className="mr-2 h-4 w-4" />
							Chat
						</Link>
					</Button>
					<span className="text-muted-foreground text-xs">
						{createdAtDate(agent.createdAt).toLocaleDateString()}
					</span>
				</div>
			</CardContent>
		</Card>
	)
}
