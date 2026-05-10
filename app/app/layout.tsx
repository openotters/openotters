import type { Metadata } from "next"
import type React from "react"
import { AppSidebar } from "@/components/app-sidebar"
import { Header } from "@/components/header"
import { QueryProvider } from "@/components/providers/query-provider"
import { ThemeProvider } from "@/components/theme-provider"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import "./globals.css"

export const metadata: Metadata = {
	title: "OpenOtters",
	description: "OCI-style build and runtime for AI agents.",
}

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
	return (
		<html lang="en" suppressHydrationWarning>
			<body className="min-h-screen font-sans antialiased">
				<ThemeProvider attribute="class" defaultTheme="dark" disableTransitionOnChange enableSystem>
					<QueryProvider>
						<SidebarProvider>
						<AppSidebar />
						<SidebarInset>
							<div data-layout-header>
								<Header />
							</div>
							<main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto overflow-x-hidden p-6">
								{children}
							</main>
							<footer
								aria-label="OpenOtters footer"
								className="flex w-full items-center justify-between border-t border-dashed px-6 py-4 text-muted-foreground text-sm"
								data-layout-footer>
								<p>
									<span>© {new Date().getFullYear()} </span>
									<a className="font-medium text-foreground hover:underline" href="/">
										OpenOtters
									</a>
								</p>
								<p>
									<span>Built by </span>
									<a
										className="font-medium text-foreground transition-colors hover:text-primary hover:underline"
										href="https://github.com/merlindorin"
										rel="noopener noreferrer"
										target="_blank">
										@merlindorin
									</a>
								</p>
							</footer>
						</SidebarInset>
					</SidebarProvider>
					</QueryProvider>
				</ThemeProvider>
			</body>
		</html>
	)
}
