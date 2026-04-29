"use client"

import Link from "next/link"
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar"

export function AppLogo() {
	return (
		<SidebarMenu>
			<SidebarMenuItem>
				<SidebarMenuButton asChild className="gap-3" size="lg">
					<Link href="/">
						<div className="flex aspect-square size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
							<svg
								aria-hidden="true"
								className="size-5"
								fill="none"
								viewBox="0 0 32 32"
								xmlns="http://www.w3.org/2000/svg">
								<title>OpenOtters</title>
								<circle cx="11" cy="13" fill="currentColor" r="2.5" />
								<circle cx="21" cy="13" fill="currentColor" r="2.5" />
								<path
									d="M10 21c1.5 1.5 3.5 2.2 6 2.2s4.5-.7 6-2.2"
									fill="none"
									stroke="currentColor"
									strokeLinecap="round"
									strokeWidth="2"
								/>
							</svg>
						</div>
						<div className="grid flex-1 text-left leading-tight">
							<span className="truncate font-semibold text-sm">OpenOtters</span>
							<span className="truncate text-muted-foreground text-xs">Agent runtime</span>
						</div>
					</Link>
				</SidebarMenuButton>
			</SidebarMenuItem>
		</SidebarMenu>
	)
}
