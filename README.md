# Discourse AI Agent Container

A Docker-based development environment for AI agents with Discourse.

## Overview

This project provides a containerized development environment that includes:
- Discourse development setup
- Essential development tools (vim, ripgrep)
- Ready-to-use database configuration, fully migrated dev/test databases
- Various AI agents ready for development (Claude, Codex, Aider, Gemini)
 - Multi-agent container management via `bin/agent`

## Prerequisites

- Docker installed on your system
- GitHub CLI (`gh`) installed and authenticated on the host

## Quick Start

1. Build the container:
   ```bash
   bin/build
   ```

2. Run the container:
   ```bash
   bin/run
   ```

3. Extract changes from container (when ready to create PR):
   ```bash
   bin/extract-changes
   ```

Optional: manage multiple named containers ("agents"):
```bash
bin/agent --new my_project   # create and select a new agent
bin/agent --list             # show all agents for this image
bin/agent --select my_project
```

## Commands

### bin/agent
```bash
bin/agent [--help|--list|--new [NAME]|--select NAME]
```

Manage named containers ("agents") created from the `ai_agent` image and track the currently selected agent in `.agent-selected` at the repo root.

**Common usage:**
- `--list` to see containers for this image (marks the selected one)
- `--new [NAME]` to create a new agent and select it
- `--select NAME` to select an existing agent (creation deferred until the next `bin/run`)

The selected agent is respected by `bin/run`, `bin/stop` and `bin/extract-changes`.

### bin/run
```bash
bin/run [--help|--reset] [command]
```

Run or attach to the ai_agent container with discourse user in `/var/www/discourse`

**Options:**
- `--help` - Show help message
- `--reset` - Stop and remove existing container before starting fresh

**Examples:**
```bash
bin/run                     # Start interactive bash session
bin/run bin/rails c         # Run Rails console
bin/run --reset             # Reset container and start bash session
bin/run --reset bin/rails s # Reset container and start Rails server
# Change host port (default 4201 -> container 4200)
HOST_PORT=4300 bin/run      # Ember CLI will be accessible on http://localhost:4300
```

**Ports:**
- By default the host port `4201` maps to container port `4200` (used by Ember CLI). Override with `HOST_PORT` and/or `CONTAINER_PORT`.
- Discourse Unicorn runs inside the container on port `9292`.

### bin/stop
```bash
bin/stop [--help]
```

Stop the ai_agent container

**Examples:**
```bash
bin/stop         # Stop the container
bin/stop --help  # Show help
```

Respects the currently selected agent (via `.agent-selected`), defaulting to `ai_agent` if none is selected.

### bin/cleanup
```bash
bin/cleanup [--help] [--all]
```

Clean up ai_agent container and optionally the image

**Options:**
- `--all` - Also remove the Docker image after removing container

**Examples:**
```bash
bin/cleanup         # Stop and remove container only
bin/cleanup --all   # Stop and remove container and image
```

Note: `bin/cleanup` targets the default container named `ai_agent`. If you created additional named agents with `bin/agent`, remove them with `docker rm <name>` (and `docker stop` if needed).

### bin/build
```bash
bin/build [docker-build-options]
```

Build the ai_agent Docker image

### bin/extract-changes
```bash
bin/extract-changes
```

Extract changes from container to local discourse/ directory

Respects the currently selected agent (via `.agent-selected`), defaulting to `ai_agent` if none is selected.

## Usage

### Building the Container

```bash
bin/build [docker-build-options]
```

The build script supports all standard Docker build options, such as:
- `--no-cache` - Build without using cache
- `--build-arg KEY=value` - Pass build arguments

### Running the Container

The container automatically starts in `/var/www/discourse` directory as the `discourse` user. See the Commands section above for detailed usage.

### Extracting Changes

```bash
bin/extract-changes
```

This command extracts changes made in the container's `/var/www/discourse` to a local `discourse/` directory, ready for manual commit and PR creation.

**Requirements:**
- Container must be running (`bin/run`)
- GitHub CLI must be installed on host

**What it does:**
1. Clones discourse/discourse to `./discourse/` (first run only)
2. Resets and cleans the local repo (subsequent runs)
3. Extracts all changes from container to local repo
4. Leaves changes ready for manual commit

**After extraction:**
```bash
cd discourse/
git status              # Review changes
git add .              # Stage changes  
git commit -m "Your commit message"
# Create PR manually with gh CLI or web interface
```

### Environment Variables

The following environment variables are automatically passed to the container if set on the host:

- `CURSOR_API_KEY` - For Cursor AI editor integration
- `ANTHROPIC_API_KEY` - For Anthropic Claude API access
- `OPENAI_API_KEY` - For OpenAI API access
- `AWS_ACCESS_KEY_ID` - For AWS services access
- `AWS_SECRET_ACCESS_KEY` - For AWS services access
- `CLAUDE_CODE_USE_BEDROCK` - Configure Claude Code to use AWS Bedrock
- `DEEPSEEK_API_KEY` - For DeepSeek API access
- `GEMINI_API_KEY` - For Google Gemini API access

Additionally, `CI=1` is always set inside the container to ensure consistent test behavior.

## Container Details

The container is based on `discourse/discourse_dev:release` and includes:
- Full Discourse development environment at `/var/www/discourse`
- Ruby/Rails stack with bundled dependencies
- Node.js with pnpm package manager
- PostgreSQL database (created and migrated)
- Cursor AI editor installation
- Development tools (vim, ripgrep)

## File Structure

```
.
├── Dockerfile          # Container definition
├── bin/
│   ├── agent          # Manage named agents (list/new/select)
│   ├── build          # Build script
│   ├── run            # Run script  
│   ├── stop           # Stop container
│   ├── cleanup        # Clean up container/image
│   └── extract-changes # Extract changes from container
├── discourse/          # Local discourse repo (created by extract-changes)
├── .agent-selected     # Tracks currently selected agent name (generated)
├── .gitignore         # Ignores discourse/ directory
└── README.md          # This file
```

## Development Workflow

1. **Initial setup:**
   ```bash
   bin/build              # Build container once
   ```

2. **Development session:**
   ```bash
   bin/run                # Start container and enter shell
   # Work with Discourse at /var/www/discourse
   ```

3. **Extract changes for PR:**
   ```bash
   bin/extract-changes    # Extract changes to local discourse/
   cd discourse/
   git add .
   git commit -m "Your commit message"
   # Create PR with gh CLI or web interface
   ```

The container persists between sessions - stopping and restarting will maintain your development state. The `discourse/` directory is ignored by git and serves as your local workspace for creating PRs.
