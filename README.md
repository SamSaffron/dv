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
│   └── run            # Run script
└── README.md          # This file
```

## Development Workflow

1. Build the container once: `bin/build`
2. Start development session: `bin/run`
3. Work with Discourse at `/var/www/discourse`

The container persists between sessions - stopping and restarting will maintain your development state.
