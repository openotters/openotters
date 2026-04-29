"use client"

import { Bot, Cpu, LayoutDashboard, Layers, Plug, Terminal } from "lucide-react"
import Link from "next/link"
import { usePathname } from "next/navigation"
import type * as React from "react"
import { AppLogo } from "@/components/app-logo"
import {
	Sidebar,
	SidebarContent,
	SidebarGroup,
	SidebarGroupContent,
	SidebarGroupLabel,
	SidebarHeader,
	SidebarMenu,
	SidebarMenuButton,
	SidebarMenuItem,
	SidebarRail,
} from "@/components/ui/sidebar"

export function AppSidebar({ ...props }: React.ComponentProps<typeof Sidebar>) {
	const pathname = usePathname()

	const isActive = (path: string) => {
		if (path === "/") {
			return pathname === "/"
		}
		return pathname === path || pathname.startsWith(`${path}/`)
	}

	const navGroups = [
		{
			title: "Overview",
			items: [{ title: "Dashboard", url: "/", icon: LayoutDashboard }],
		},
		{
			title: "Agents",
			items: [
				{ title: "Agents", url: "/agents", icon: Bot },
				{ title: "Images", url: "/images", icon: Layers },
				{ title: "Bins", url: "/bins", icon: Terminal },
				{ title: "Providers", url: "/providers", icon: Plug },
				{ title: "Models", url: "/models", icon: Cpu },
			],
		},
	]

	return (
		<Sidebar collapsible="icon" {...props}>
			<SidebarHeader>
				<AppLogo />
			</SidebarHeader>
			<SidebarContent>
				{navGroups.map((group) => (
					<SidebarGroup key={group.title}>
						<SidebarGroupLabel>{group.title}</SidebarGroupLabel>
						<SidebarGroupContent>
							<SidebarMenu>
								{group.items.map((item) => (
									<SidebarMenuItem key={item.title}>
										<SidebarMenuButton asChild isActive={isActive(item.url)} tooltip={item.title}>
											<Link href={item.url}>
												<item.icon />
												<span>{item.title}</span>
											</Link>
										</SidebarMenuButton>
									</SidebarMenuItem>
								))}
							</SidebarMenu>
						</SidebarGroupContent>
					</SidebarGroup>
				))}
			</SidebarContent>
			<SidebarRail />
		</Sidebar>
	)
}
