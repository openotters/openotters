"use client"

import { ArrowDownUp } from "lucide-react"
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select"

export interface SortOption {
	// Stable identifier the page maps back to a comparator.
	id: string
	label: string
}

interface SortSelectProps {
	value: string
	onValueChange: (id: string) => void
	options: SortOption[]
	className?: string
}

export function SortSelect({ value, onValueChange, options, className }: SortSelectProps) {
	return (
		<Select onValueChange={onValueChange} value={value}>
			<SelectTrigger className={className}>
				<ArrowDownUp className="mr-2 h-4 w-4" />
				<SelectValue placeholder="Sort" />
			</SelectTrigger>
			<SelectContent>
				{options.map((opt) => (
					<SelectItem key={opt.id} value={opt.id}>
						{opt.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	)
}
