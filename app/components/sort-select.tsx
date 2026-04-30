"use client"

import { ArrowDownUp } from "lucide-react"
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select"

export const SORT_DEFAULT_ID = "default"

export interface SortOption {
	// Stable identifier the page maps back to a comparator. The id
	// "default" is reserved for the implicit no-sort entry that
	// `useStableSort` interprets as preserved-insertion-order.
	id: string
	label: string
}

interface SortSelectProps {
	value: string
	onValueChange: (id: string) => void
	// Options exclusive of the implicit "Default" entry. The "Default"
	// option is always present at the top of the dropdown.
	options: SortOption[]
	// Optional override for the default-row label. Defaults to "Default
	// (insertion order)" — descriptive enough that a user can tell why
	// the rows aren't alphabetised.
	defaultLabel?: string
	className?: string
}

export function SortSelect({
	value,
	onValueChange,
	options,
	defaultLabel = "Default (insertion order)",
	className,
}: SortSelectProps) {
	return (
		<Select onValueChange={onValueChange} value={value}>
			<SelectTrigger className={className}>
				<ArrowDownUp className="mr-2 h-4 w-4" />
				<SelectValue placeholder="Sort" />
			</SelectTrigger>
			<SelectContent>
				<SelectItem value={SORT_DEFAULT_ID}>{defaultLabel}</SelectItem>
				{options.map((opt) => (
					<SelectItem key={opt.id} value={opt.id}>
						{opt.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	)
}
