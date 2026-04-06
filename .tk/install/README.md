# Install Taskfile

> The provided Taskfile is intended to be a reusable component that provides install and uninstall task orchestration for various repositories.

<!-- TOC -->
* [Install Taskfile](#install-taskfile)
  * [Summary](#summary)
  * [Prerequisites](#prerequisites)
  * [Configuration](#configuration)
  * [Usage](#usage)
    * [Installing Dependencies](#installing-dependencies)
    * [Uninstalling Dependencies](#uninstalling-dependencies)
    * [Examples](#examples)
<!-- TOC -->

## Summary

This `Taskfile` provides a set of tasks that facilitate running install and uninstall commands by pattern matching task names. It enables you to orchestrate multiple installation and uninstallation tasks across your project by simply running a single command. This is particularly useful when including multiple `Taskfile` components that each provide their own `:install` and `:uninstall` tasks.

## Prerequisites

To get started with using this Taskfile, ensure you have the following prerequisites installed:

- [Task](https://taskfile.dev/): A task runner / simpler Make alternative written in Go.
- [jq](https://stedolan.github.io/jq/): A lightweight and flexible command-line JSON processor.

These tools need to be installed and available in your system's PATH to execute the tasks defined in the Taskfile properly.

## Configuration

The Taskfile uses variables that can be customized for different scenarios. By default, it introduces the following variables:

- `INSTALL_DEFAULT_PATTERN`: This pattern defines the regular expression used to match install task names. The default value is `.*:install`.
- `UNINSTALL_DEFAULT_PATTERN`: This pattern defines the regular expression used to match uninstall task names. The default value is `.*:uninstall`.

You can override these patterns by redefining them in your environment or in your `Taskfile`.

## Usage

This Taskfile provides two main tasks: `install` and `uninstall`.

### Installing Dependencies

The `install` task runs all tasks in your Taskfile that match the install pattern (default: `.*:install`).

**Variables:**
- `PATTERN`: Specifies the task name pattern to match against when running the install tasks. The default value is `{{.INSTALL_DEFAULT_PATTERN}}`, which can be overridden at runtime.

**Usage:**
- `task install` - Runs all install tasks that match the default pattern
- `PATTERN="custom:.*" task install` - Runs install tasks matching a custom pattern
- `task install -- -f` - Passes extra arguments to all install commands

### Uninstalling Dependencies

The `uninstall` task runs all tasks in your Taskfile that match the uninstall pattern (default: `.*:uninstall`).

**Variables:**
- `PATTERN`: Specifies the task name pattern to match against when running the uninstall tasks. The default value is `{{.UNINSTALL_DEFAULT_PATTERN}}`, which can be overridden at runtime.

**Usage:**
- `task uninstall` - Runs all uninstall tasks that match the default pattern
- `PATTERN="custom:.*" task uninstall` - Runs uninstall tasks matching a custom pattern
- `task uninstall -- -f` - Passes extra arguments to all uninstall commands

### Examples

Assuming you have the following `Taskfile.yaml` in your project:

```yaml
version: '3'

includes:
  install: https://raw.githubusercontent.com/example/taskfile-install/main/tasks.yaml
  golangci: https://raw.githubusercontent.com/example/taskfile-golangci/main/tasks.yaml
  goreleaser: https://raw.githubusercontent.com/example/taskfile-goreleaser/main/tasks.yaml

tasks:
  default:
    cmds:
      - task: install
```

When you run `task install`, it will automatically execute:
- `golangci:install`
- `goreleaser:install`

And any other tasks that end with `:install` from your included Taskfiles.
