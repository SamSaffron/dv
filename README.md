# Discourse AI Agent Container

A Docker-based development environment for AI agents with Discourse.

## Overview

This project provides a containerized development environment that includes:
- Discourse development setup
- Essential developer tools (vim, ripgrep)
- Ready-to-use database configuration, fully migrated dev/test databases
- Various AI helpers preinstalled in the image (Claude, Codex, Aider, Gemini)
- Multi-agent container management via `dv` top-level commands (`list`, `new`, `select`, `rename`) and the `agent` group
 - Embedded Dockerfile managed by the CLI with safe override mechanisms

## Prerequisites

- Docker installed on your system
- Go 1.22+
- Optional: GitHub CLI (`gh`) if you want to use `dv extract`’s default cloning behavior

## Quick Start

1. Build the `dv` binary:
   ```bash
   go build
   ```

2. Build the Docker image:
   ```bash
   ./dv build
   ```

3. Start the container:
   ```bash
   ./dv start
   ```
4. Enter the container (interactive shell or run a command):
   ```bash
   ./dv enter
   # or to run a one-off command
   ./dv enter -- bin/rails c
   ```
5. Extract changes from the container (when ready to create a PR):
   ```bash
   ./dv extract
   ```

Optional: manage multiple named containers ("agents"):
```bash
./dv new my_project     # create and select a new agent
./dv list               # show all agents for the selected image
./dv select my_project  # select an existing agent
./dv rename old new     # rename an agent
```

## dv Commands

### dv build
Build the Docker image (defaults to tag `ai_agent`).

```bash
./dv build [--no-cache] [--build-arg KEY=VAL] [--rm-existing]
```

Notes:
- Uses an embedded `Dockerfile` managed under your XDG config directory. On each build, the CLI ensures the materialized `Dockerfile` matches the embedded version via a SHA file.
- Override precedence:
  1) `DV_DOCKERFILE=/absolute/path/to/Dockerfile`
  2) `${XDG_CONFIG_HOME}/dv/Dockerfile.local`
  3) Embedded default (materialized to `${XDG_CONFIG_HOME}/dv/Dockerfile`)
  The command prints which Dockerfile path it used.

### dv start
Create or start the container for the selected image (no shell).

```bash
./dv start [--reset] [--name NAME] [--image NAME] [--host-starting-port N] [--container-port N]
```

Notes:
- Maps host `4201` → container `4200` by default (Ember CLI dev server). Override with flags.
- Performs a pre-flight check and picks the next free port if needed.

### dv enter
Attach to the running container as user `discourse` in `/var/www/discourse`, or run a one-off command.

```bash
./dv enter [--name NAME] [-- cmd ...]
```

Notes:
- Always sets `CI=1` and passes through common API keys from your environment.

### dv stop
Stop the selected or specified container.

```bash
./dv stop [--name NAME]
```

### dv remove
Remove the container and optionally the image.

```bash
./dv remove [--image] [--name NAME]
```

### Agent management
Manage multiple containers for the selected image; selection is stored in XDG config. These are the preferred top-level commands; the old `dv agent` group has been removed.

```bash
./dv list
./dv new [NAME]
./dv select NAME
./dv rename OLD NEW
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

### dv completion
Generate shell completion scripts. For zsh:

```bash
./dv completion zsh           # print to stdout
./dv completion zsh --install # install to ~/.local/share/zsh/site-functions/_dv
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
 - Playwright and system deps preinstalled

## File Structure

```
.
├── internal/
│   └── assets/
│       ├── Dockerfile      # Embedded container definition used by dv build
│       └── dockerfile.go   # Embed/resolve logic (env + XDG overrides)
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
   ./dv start
   ./dv enter
   # Work with Discourse at /var/www/discourse
   ```
3. Extract changes to a local clone and commit:
   ```bash
   ./dv extract
   cd $(./dv data)/discourse_src
   git add . && git commit -m "Your message"
   ```

