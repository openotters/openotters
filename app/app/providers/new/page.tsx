import { ArrowLeft } from "lucide-react"
import Link from "next/link"
import { ProviderForm } from "@/components/provider-form"
import { Button } from "@/components/ui/button"

export default function NewProviderPage() {
	return (
		<div className="space-y-6">
			<div className="flex items-center gap-4">
				<Button asChild size="icon" variant="ghost">
					<Link href="/providers">
						<ArrowLeft className="h-4 w-4" />
					</Link>
				</Button>
				<div>
					<h1 className="font-semibold text-2xl tracking-tight">Add Provider</h1>
					<p className="text-muted-foreground">
						Configure a new LLM provider. Saved entries land in{" "}
						<code className="font-mono text-xs">~/.otters/providers.yaml</code>.
					</p>
				</div>
			</div>
			<ProviderForm mode="create" />
		</div>
	)
}
