# AGENT.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`openotters-app` is the Next.js frontend for [OpenOtters](https://github.com/openotters/openotters), an OCI-style build and runtime for AI agents. The project was bootstrapped from the [`sshark-app`](https://github.com/merlindorin/sshark-app) template.

## Commands

```bash
npm run dev          # Start development server (localhost:3000)
npm run build        # Build for production
npm run lint         # Run ESLint
npm run storybook    # Start Storybook on port 6006
npm exec -- ultracite fix    # Format code with Biome
npm exec -- ultracite check  # Check for linting issues
```

## Architecture

### Directory Structure
```
app/                  # Next.js App Router pages, layout, globals.css
components/           # React components (atomic design)
├── molecules/        # Composed components (mode-toggle)
├── providers/        # React context providers (query, theme)
├── templates/        # Layout templates (navbar, footer)
└── ui/               # shadcn/ui primitives (new-york style)
content/docs/         # MDX documentation content (fumadocs)
lib/                  # Utilities and shared code
helm/openotters-app/  # Helm chart for Kubernetes deployment
public/               # Static assets
terraform/            # Terraform IaC
```

### API Integration
- In dev, the app can proxy `/api/*` to a backend by setting `API_URL` (see `next.config.mts`).
- In Kubernetes, the ingress routes `/api` to a backend service configured in `helm/openotters-app/values.yaml`.

### Component Structure
Components follow atomic design principles:
- `components/molecules/` - Composed components
- `components/templates/` - Layout templates (navbar, footer)
- `components/providers/` - React context providers (query, theme)
- `components/ui/` - shadcn/ui components (new-york style)

### Data Fetching
- Uses [TanStack Query](https://tanstack.com/query) for server state.
- Add custom hooks under `hooks/` (directory not yet created — add it when needed).

## Release Workflow

1. **Commit changes**
   ```bash
   git add <files> && git commit -m "feat/fix: message"
   ```

2. **Update Helm chart** (`helm/openotters-app/Chart.yaml`)
   - Bump `version` and `appVersion` to match the new release.

3. **Commit chart update**
   ```bash
   git add helm/openotters-app/Chart.yaml && git commit -m "chore: bump chart version to 0.x.x"
   ```

4. **Tag and push**
   ```bash
   git tag v0.x.x && git push && git push --tags
   ```

5. **GitHub Actions** builds and pushes the Docker image (configure the registry in `.github/workflows/`).
