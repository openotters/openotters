"use client"

import { useQuery } from "@connectrpc/connect-query"
import { Copy, Eye, EyeOff } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import { getAgentIdentity } from "@/lib/proto/v1/daemon-Runtime_connectquery"

interface IdentityPanelProps {
	agentRef: string
}

function formatUnixSeconds(seconds: bigint): string {
	if (seconds === 0n) {
		return "—"
	}
	return new Date(Number(seconds) * 1000).toLocaleString()
}

function relativeTo(seconds: bigint): string {
	if (seconds === 0n) {
		return ""
	}
	const diffMs = Number(seconds) * 1000 - Date.now()
	const absMs = Math.abs(diffMs)
	const minute = 60_000
	const hour = 60 * minute
	const day = 24 * hour
	const year = 365 * day
	let value: number
	let unit: string
	if (absMs >= year) {
		value = absMs / year
		unit = "year"
	} else if (absMs >= day) {
		value = absMs / day
		unit = "day"
	} else if (absMs >= hour) {
		value = absMs / hour
		unit = "hour"
	} else {
		value = absMs / minute
		unit = "minute"
	}
	const rounded = value >= 10 ? Math.round(value) : Math.round(value * 10) / 10
	const suffix = rounded === 1 ? unit : `${unit}s`
	return diffMs >= 0 ? `in ${rounded} ${suffix}` : `${rounded} ${suffix} ago`
}

// IdentityPanel renders the agent's JWT — decoded claims plus a
// click-to-reveal raw-token block. Operator-only on the server side
// (GetAgentIdentity rejects agent tokens); the UI is already auth-
// gated so the surface is no worse than reading daemon.db directly.
export function IdentityPanel({ agentRef }: IdentityPanelProps) {
	const identity = useQuery(getAgentIdentity, { ref: agentRef })
	const [tokenVisible, setTokenVisible] = useState(false)

	if (identity.isLoading) {
		return <p className="text-muted-foreground">Loading identity…</p>
	}
	if (identity.error) {
		return (
			<Card>
				<CardHeader>
					<CardTitle className="text-base">Identity</CardTitle>
					<CardDescription>
						Failed to read the agent's JWT: {identity.error.message}
					</CardDescription>
				</CardHeader>
			</Card>
		)
	}

	const token = identity.data?.token ?? ""
	const claims = identity.data?.claims

	const handleCopy = async () => {
		if (token === "") {
			return
		}
		try {
			await navigator.clipboard.writeText(token)
			toast.success("Token copied to clipboard")
		} catch (err) {
			toast.error("Copy failed", {
				description: err instanceof Error ? err.message : String(err),
			})
		}
	}

	return (
		<div className="space-y-4">
			<Card>
				<CardHeader>
					<CardTitle className="text-base">Decoded claims</CardTitle>
					<CardDescription>
						Parsed from the JWT stored alongside the agent. The runtime
						authenticates to the daemon with this token on every call.
					</CardDescription>
				</CardHeader>
				<CardContent className="space-y-3 text-sm">
					{claims ? (
						<>
							<Row label="Issuer" mono value={claims.issuer || "—"} />
							<Separator />
							<Row label="Agent ref" mono value={claims.agentRef || "—"} />
							<Separator />
							<Row label="JTI" mono truncate value={claims.jti || "—"} />
							<Separator />
							<Row
								label="Issued at"
								value={`${formatUnixSeconds(claims.issuedAt)} (${relativeTo(claims.issuedAt)})`}
							/>
							<Separator />
							<Row
								label="Expires at"
								value={`${formatUnixSeconds(claims.expiresAt)} (${relativeTo(claims.expiresAt)})`}
							/>
							<Separator />
							<Row
								label="Links"
								value={
									claims.links.length === 0
										? "(no outbound calls allowed)"
										: `${claims.links.length} target${claims.links.length === 1 ? "" : "s"}`
								}
							/>
							{claims.links.length > 0 && (
								<div className="space-y-1 pl-2">
									{claims.links.map((id) => (
										<p className="font-mono text-muted-foreground text-xs" key={id}>
											{id}
										</p>
									))}
								</div>
							)}
						</>
					) : (
						<p className="text-muted-foreground">
							Claims unavailable (token may be revoked or the signing key has rotated).
						</p>
					)}
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle className="text-base">Raw token</CardTitle>
					<CardDescription>
						The signed JWT — anyone with this string can call the daemon as this
						agent until exp.
					</CardDescription>
				</CardHeader>
				<CardContent>
					{token === "" ? (
						<p className="text-muted-foreground">
							No token persisted. The daemon may have been built without a signing
							key (test runtime), or this agent predates token issuance.
						</p>
					) : (
						<div className="flex items-center gap-2">
							<Input
								className="font-mono text-xs"
								readOnly
								type={tokenVisible ? "text" : "password"}
								value={token}
							/>
							<Button
								onClick={() => setTokenVisible((v) => !v)}
								size="icon"
								title={tokenVisible ? "Hide token" : "Show token"}
								variant="outline">
								{tokenVisible ? (
									<EyeOff className="h-4 w-4" />
								) : (
									<Eye className="h-4 w-4" />
								)}
							</Button>
							<Button onClick={handleCopy} size="icon" title="Copy token" variant="outline">
								<Copy className="h-4 w-4" />
							</Button>
						</div>
					)}
				</CardContent>
			</Card>
		</div>
	)
}

interface RowProps {
	label: string
	value: string
	mono?: boolean
	truncate?: boolean
}

function Row({ label, value, mono, truncate }: RowProps) {
	return (
		<div className="flex items-baseline justify-between gap-3">
			<span className="text-muted-foreground">{label}</span>
			<span
				className={`text-right ${mono ? "font-mono text-xs" : ""} ${truncate ? "truncate" : ""}`}>
				{value}
			</span>
		</div>
	)
}
