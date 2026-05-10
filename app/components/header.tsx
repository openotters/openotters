"use client"

import { ChevronRight } from "lucide-react"
import Link from "next/link"
import { usePathname } from "next/navigation"
import { Fragment } from "react"
import { DaemonStatus } from "@/components/daemon-status"
import {
	Breadcrumb,
	BreadcrumbItem,
	BreadcrumbLink,
	BreadcrumbList,
	BreadcrumbPage,
	BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"
import { Separator } from "@/components/ui/separator"
import { SidebarTrigger } from "@/components/ui/sidebar"

function decodeSegment(segment: string): string {
	try {
		return decodeURIComponent(segment)
	} catch {
		return segment
	}
}

// Identifier segments are opaque values like an OCI ref or an agent
// id — render them verbatim (mono font, no title-casing). Nav segments
// like "images" or "agents-new" get the friendly title-case treatment.
function isIdentifier(segment: string): boolean {
	return /[/:.]/.test(segment)
}

function formatNavSegment(segment: string): string {
	return segment
		.split("-")
		.map((word) => word.charAt(0).toUpperCase() + word.slice(1))
		.join(" ")
}

export function Header() {
	const pathname = usePathname()
	const segments = pathname.split("/").filter(Boolean)

	return (
		<header className="flex h-16 shrink-0 items-center gap-2 border-b border-dashed px-4">
			<SidebarTrigger className="-ml-1" />
			<Separator className="mr-2 h-4" orientation="vertical" />
			<Breadcrumb className="flex-1">
				<BreadcrumbList>
					<BreadcrumbItem>
						{segments.length === 0 ? (
							<BreadcrumbPage>Dashboard</BreadcrumbPage>
						) : (
							<BreadcrumbLink asChild>
								<Link href="/">Dashboard</Link>
							</BreadcrumbLink>
						)}
					</BreadcrumbItem>
					{segments.map((segment, index) => {
						const isLast = index === segments.length - 1
						const href = `/${segments.slice(0, index + 1).join("/")}`
						const decoded = decodeSegment(segment)
						const identifier = isIdentifier(decoded)
						const label = identifier ? decoded : formatNavSegment(decoded)
						const className = identifier
							? "max-w-[40ch] truncate font-mono text-sm"
							: undefined
						return (
							<Fragment key={segment}>
								<BreadcrumbSeparator>
									<ChevronRight className="h-4 w-4" />
								</BreadcrumbSeparator>
								<BreadcrumbItem>
									{isLast ? (
										<BreadcrumbPage className={className} title={identifier ? decoded : undefined}>
											{label}
										</BreadcrumbPage>
									) : (
										<BreadcrumbLink asChild>
											<Link className={className} href={href} title={identifier ? decoded : undefined}>
												{label}
											</Link>
										</BreadcrumbLink>
									)}
								</BreadcrumbItem>
							</Fragment>
						)
					})}
				</BreadcrumbList>
			</Breadcrumb>
			<DaemonStatus />
		</header>
	)
}
