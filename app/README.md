## openotters-app

Next.js frontend for OpenOtters, bootstrapped from the [`sshark-app`](https://github.com/merlindorin/sshark-app) template.

## Getting Started

```bash
npm install
npm run dev
```

Open <http://localhost:3000>.

## Stack

- [Next.js 16](https://nextjs.org) (App Router, Turbopack, standalone output)
- [React 19](https://react.dev) + TypeScript
- [Tailwind CSS v4](https://tailwindcss.com) + [shadcn/ui](https://ui.shadcn.com) (new-york)
- [Biome](https://biomejs.dev) via [ultracite](https://www.ultracite.ai/)
- [TanStack Query](https://tanstack.com/query)
- [Storybook 10](https://storybook.js.org)
- [Fumadocs](https://fumadocs.dev) (MDX docs in `content/docs`)
- [Husky](https://typicode.github.io/husky/) for git hooks

## Scripts

```bash
npm run dev          # Dev server (http://localhost:3000)
npm run build        # Production build
npm run start        # Start production server
npm run lint         # ESLint
npm run storybook    # Storybook (http://localhost:6006)
npm exec -- ultracite fix    # Format with Biome
npm exec -- ultracite check  # Lint with Biome
```

## Layout

```
app/                 # Next.js App Router (pages, layout, globals.css)
components/
├── molecules/       # Composed components (mode-toggle)
├── providers/       # React context providers (query, theme)
├── templates/       # Layout pieces (navbar, footer)
└── ui/              # shadcn/ui primitives (new-york)
content/docs/        # MDX documentation (fumadocs)
lib/                 # Utilities (cn, layout-shared)
public/              # Static assets
helm/openotters-app/ # Kubernetes Helm chart
terraform/           # Infrastructure as code
```
