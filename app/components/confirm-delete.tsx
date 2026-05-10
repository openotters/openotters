"use client"

import type { ReactNode } from "react"
import { useState } from "react"
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/ui/alert-dialog"

interface ConfirmDeleteProps {
	title: string
	description: ReactNode
	// Controls the rendered trigger. Receives an `open` callback the
	// caller wires to whatever element launches the confirmation
	// (button, dropdown item, icon, etc.). Kept as a render-prop so
	// the trigger can be a DropdownMenuItem with onSelect=preventDefault
	// (close menu only) or a plain button — without coupling this
	// component to either.
	trigger: (open: () => void) => ReactNode
	confirmLabel?: string
	pending?: boolean
	onConfirm: () => void
}

export function ConfirmDelete({
	title,
	description,
	trigger,
	confirmLabel = "Delete",
	pending,
	onConfirm,
}: ConfirmDeleteProps) {
	const [open, setOpen] = useState(false)

	return (
		<>
			{trigger(() => setOpen(true))}
			<AlertDialog onOpenChange={setOpen} open={open}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>{title}</AlertDialogTitle>
						<AlertDialogDescription asChild>
							<div>{description}</div>
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel disabled={pending}>Cancel</AlertDialogCancel>
						<AlertDialogAction
							className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
							disabled={pending}
							onClick={(e) => {
								e.preventDefault()
								onConfirm()
								setOpen(false)
							}}>
							{confirmLabel}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</>
	)
}
