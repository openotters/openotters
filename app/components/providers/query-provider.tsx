"use client"

import { TransportProvider } from "@connectrpc/connect-query"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { ReactQueryDevtools } from "@tanstack/react-query-devtools"
import { useState } from "react"
import { toast } from "sonner"
import { Toaster } from "@/components/ui/sonner"
import { transport } from "@/lib/transport"

// Mounted once at the root layout. Owns the daemon transport (used by
// every connect-query hook) and the TanStack Query cache. Co-located
// here is the global mutation error toast — every useMutation gets a
// surfaced error without each call site wiring its own onError.
export function QueryProvider({ children }: { children: React.ReactNode }) {
	const [queryClient] = useState(
		() =>
			new QueryClient({
				defaultOptions: {
					queries: {
						refetchInterval: 5_000,
						refetchOnWindowFocus: true,
						retry: 1,
					},
					mutations: {
						onError: (error) => {
							const message =
								error instanceof Error ? error.message : "Mutation failed"
							toast.error(message)
						},
					},
				},
			}),
	)

	return (
		<TransportProvider transport={transport}>
			<QueryClientProvider client={queryClient}>
				{children}
				<Toaster position="top-center" richColors />
				{process.env.NODE_ENV !== "production" && <ReactQueryDevtools initialIsOpen={false} />}
			</QueryClientProvider>
		</TransportProvider>
	)
}
