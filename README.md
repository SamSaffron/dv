# Agent Container

A Docker-based development environment for AI agents with Discourse integration and Cursor editor support.

## Overview

This project provides a containerized development environment that includes:
- Discourse development setup
- Essential development tools (vim, ripgrep)
- Ready-to-use database configuration
- Various AI agents ready for development

## Prerequisites

- Docker installed on your system
- GitHub CLI (`gh`) installed and authenticated on the host
- CURSOR_API_KEY environment variable (optional, for Cursor integration)

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

## Usage

### Building the Container

```bash
bin/build [docker-build-options]
```

The build script supports all standard Docker build options, such as:
- `--no-cache` - Build without using cache
- `--build-arg KEY=value` - Pass build arguments

### Running the Container

```bash
bin/run [command]
```

- Without arguments: Opens an interactive bash shell as the `discourse` user
- With command: Executes the specified command in the container

The container automatically:
- Mounts the current directory to `/workspace` inside the container
- Starts in detached mode if not already running
- Connects to existing container if already running

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

- `CURSOR_API_KEY` - Automatically passed to the container if set on the host

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
│   ├── build          # Build script
│   ├── run            # Run script
│   └── extract-changes # Extract changes from container
├── discourse/          # Local discourse repo (created by extract-changes)
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
