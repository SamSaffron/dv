# Discourse AI Agent Container

A Docker-based development environment for AI agents with Discourse. A Go CLI named `dv` replaces the legacy `bin/*` scripts and stores config/data under XDG paths.

## Overview

This project provides a containerized development environment that includes:
- Discourse development setup
- Essential developer tools (vim, ripgrep)
- Ready-to-use database configuration, fully migrated dev/test databases
- Various AI helpers preinstalled in the image (Claude, Codex, Aider, Gemini)
- Multi-agent container management via `dv agent`

## Prerequisites

- Docker installed on your system
- Go 1.22+
- Optional: GitHub CLI (`gh`) if you want to use `dv extract`’s default cloning behavior

## Quick Start

1. Build the `dv` binary:
   ```bash
   go build -o dv ./cmd/dv
   ```

2. Build the Docker image:
   ```bash
   ./dv build
   ```

3. Run the container and enter a shell:
   ```bash
   ./dv run
   ```

4. Extract changes from the container (when ready to create a PR):
   ```bash
   ./dv extract
   ```

Optional: manage multiple named containers ("agents"):
```bash
./dv agent new my_project   # create and select a new agent
./dv agent list             # show all agents for this image
./dv agent select my_project
```

## dv Commands

### dv build
Build the Docker image (defaults to tag `ai_agent`).

```bash
./dv build [--no-cache] [--build-arg KEY=VAL] [--rm-existing]
```

### dv run
Create/start the container and attach as user `discourse` in `/var/www/discourse`.

```bash
./dv run [--reset] [--name NAME] [--host-port N] [--container-port N] [-- cmd ...]
```

Notes:
- Maps host `4201` → container `4200` by default (Ember CLI dev server). Override with flags.
- Always sets `CI=1` and passes through common API keys from your environment.

### dv stop
Stop the selected or specified container.

```bash
./dv stop [--name NAME]
```

### dv cleanup
Remove the container and optionally the image.

```bash
./dv cleanup [--all] [--name NAME]
```

### dv agent
Manage multiple containers for this image; selection is stored in XDG config.

```bash
./dv agent list
./dv agent new [NAME]
./dv agent select NAME
```

### dv extract
Copy modified files from the running container’s `/var/www/discourse` into a local clone and create a new branch at the container’s HEAD.

```bash
./dv extract [--name NAME]
```

By default, the destination is `${XDG_DATA_HOME}/dv/discourse_src`.

### dv config
Read/write config stored at `${XDG_CONFIG_HOME}/dv/config.json`.

```bash
./dv config get KEY
./dv config set KEY VALUE
./dv config show
```

### dv data
Print the data directory path (`${XDG_DATA_HOME}/dv`).

```bash
./dv data
```

## Environment Variables

Automatically passed through when set on the host:

- `CURSOR_API_KEY`
- `ANTHROPIC_API_KEY`
- `OPENAI_API_KEY`
- `AWS_ACCESS_KEY_ID`
- `AWS_SECRET_ACCESS_KEY`
- `CLAUDE_CODE_USE_BEDROCK`
- `DEEPSEEK_API_KEY`
- `GEMINI_API_KEY`

Additionally, `CI=1` is always set inside the container to ensure consistent test behavior.

## Container Details

The image is based on `discourse/discourse_dev:release` and includes:
- Full Discourse development environment at `/var/www/discourse`
- Ruby/Rails stack with bundled dependencies
- Node.js (pnpm) + Ember CLI dev server
- Databases created and migrated for dev/test
- Development tools (vim, ripgrep)
- Helper tools installed for code agents

## File Structure

```
.
├── Dockerfile              # Container definition
├── cmd/
│   └── dv/                 # dv binary entrypoint
├── internal/
│   ├── cli/                # dv subcommands (build, run, stop, ...)
│   ├── config/             # JSON config load/save
│   ├── docker/             # Docker CLI wrappers
│   └── xdg/                # XDG path helpers
├── bin/                    # Legacy bash scripts (being replaced by dv)
├── README.md
└── ai-agents.md            # Guidance for AI agents contributing here
```

## Development Workflow (using dv)

1. Build image:
   ```bash
   ./dv build
   ```
2. Develop inside the container:
   ```bash
   ./dv run
   # Work with Discourse at /var/www/discourse
   ```
3. Extract changes to a local clone and commit:
   ```bash
   ./dv extract
   cd $(./dv data)/discourse_src
   git add . && git commit -m "Your message"
   ```

## Legacy scripts

The `bin/*` scripts remain for continuity but are being superseded by `dv`. Prefer `dv` for all workflows.
