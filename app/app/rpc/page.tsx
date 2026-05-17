"use client"

import { PageHeader } from "@/components/page-header"
import { RpcMonitor } from "@/components/rpc/rpc-monitor"

export default function RPCPage() {
	return (
		<div className="space-y-6">
			<PageHeader
				description="Live tap of every Connect call hitting the daemon. The recorder runs as an HTTP middleware OUTSIDE the auth interceptor, so every request — including auth-failed ones and any RPC added in the future — is captured automatically."
				title="RPC monitor"
			/>
			<RpcMonitor />
		</div>
	)
}
