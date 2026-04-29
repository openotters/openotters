import type { StorybookConfig } from "@storybook/nextjs-vite"

const config: StorybookConfig = {
	stories: ["../components/**/*.stories.@(js|jsx|mjs|ts|tsx)"],
	addons: ["@storybook/addon-themes"],
	framework: "@storybook/nextjs-vite",
	staticDirs: ["../public"],
}
export default config
