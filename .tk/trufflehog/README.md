# Trufflehog Taskfile

> This repository contains a reusable Taskfile that provides secret scanning capabilities using TruffleHog for various Git projects.

<!-- TOC -->
* [Trufflehog Taskfile](#trufflehog-taskfile)
  * [Summary](#summary)
  * [Prerequisites](#prerequisites)
  * [Configuration](#configuration)
  * [Available Tasks](#available-tasks)
  * [Usage](#usage)
    * [Local Secret Scanning](#local-secret-scanning)
    * [CI/CD Integration](#cicd-integration)
    * [Examples](#examples)
<!-- TOC -->

## Summary

This `Taskfile` provides integration with [TruffleHog](https://github.com/trufflesecurity/trufflehog), a tool for finding secrets accidentally committed to git repositories. It includes tasks for:
- Installing and managing TruffleHog as a Go tool
- Running secret scans locally on your repository
- Generating GitHub Actions workflow configurations for automated secret scanning
- Integration with pre-commit hooks

The tasks are configured to use widely accepted best practices for secret detection and prevention in version control systems.

## Prerequisites

To get started with using this Taskfile, ensure you have the following prerequisites installed:

- [Task](https://taskfile.dev/): A task runner / simpler Make alternative written in Go.
- [Go](https://golang.org/): The Go programming language (required to install TruffleHog as a Go tool).

TruffleHog will be installed automatically when you run the install task.

## Configuration

The Taskfile uses the following default variables that can be customized:

- `DEFAULT_GO_BIN`: The Go binary to use (default: `go`)
- `DEFAULT_TRUFFLEHOG_PACKAGE`: The TruffleHog package path (default: `github.com/trufflesecurity/trufflehog/v3`)
- `DEFAULT_TRUFFLEHOG_BIN_NAME`: The TruffleHog binary name (default: `trufflehog`)
- `DEFAULT_TRUFFLEHOG_VERSION`: The version to install (default: `latest`)
- `DEFAULT_TRUFFLEHOG_GITHUBACTION_FILENAME`: GitHub Actions workflow file path (default: `.github/workflows/trufflehog.yml`)

You can override these variables in your environment or in your main `Taskfile`.

## Available Tasks

Here are the tasks that the `Taskfile` provides:

- `install`: Installs TruffleHog as a Go tool
- `uninstall`: Removes TruffleHog from Go tools
- `run`: Runs TruffleHog with custom arguments
- `local`: Runs secret detection on the current repository (verified secrets only)
- `ci`: Generates a GitHub Actions workflow for automated secret scanning
- `pre-commit`: Alias for the `local` task, suitable for pre-commit hooks
- `default`: Runs both `ci` and `local` tasks

## Usage

To use this `Taskfile`, include it in your project's `Taskfile`. For example:

```yaml
version: '3'

includes:
  trufflehog: https://raw.githubusercontent.com/example/taskfile-trufflehog/main/tasks.yaml
```

### Local Secret Scanning

To scan your repository for secrets locally:

```shell
task trufflehog:local
```

This will scan the current git repository for verified secrets only and fail if any are found.

### CI/CD Integration

To generate a GitHub Actions workflow for automated secret scanning:

```shell
task trufflehog:ci
```

This creates a workflow file at `.github/workflows/trufflehog.yml` that will:
- Run on pull requests, pushes, and daily at 4 AM
- Scan the repository for verified secrets
- Fail the workflow if secrets are detected

### Examples

**Installing TruffleHog:**
```shell
task trufflehog:install
```

**Running with custom arguments:**
```shell
task trufflehog:run -- --help
```

**Scanning with custom TruffleHog arguments:**
```shell
TRUFFLEHOG_ARGS="--json" task trufflehog:run
```

**Uninstalling TruffleHog:**
```shell
task trufflehog:uninstall
```
