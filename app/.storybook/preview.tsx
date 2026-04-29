import { withThemeByClassName } from "@storybook/addon-themes"
import type { Decorator, Preview } from "@storybook/nextjs-vite"
import ThemeProvider from "@/components/providers/theme-provider"

import "../app/globals.css"

const withThemeProvider: Decorator = (Decoration) => (
	<ThemeProvider attribute="class" defaultTheme="dark" enableSystem>
		<Decoration />
	</ThemeProvider>
)

const preview: Preview = {
	parameters: {
		controls: {
			expanded: true,
			matchers: {
				color: /(background|color)$/i,
				date: /Date$/i,
			},
		},
	},
	decorators: [
		withThemeProvider,
		withThemeByClassName<Renderer>({
			themes: {
				light: "",
				dark: "dark",
			},
			defaultTheme: "light",
		}),
	],
}

export default preview
