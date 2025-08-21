# AI Agents Guide

This guide documents the repository purpose, key files, and operational guidelines for autonomous agents. The primary interface is the `dv` Go CLI.

## Repository Purpose
- Provision Discourse development environment via Docker
- Provide CLI (`dv`) to manage container lifecycle and export code changes to host Git workflow

## Key Files and Directories
- `internal/assets/Dockerfile`: Embedded base image definition
- `internal/assets/dockerfile.go`: Dockerfile resolution logic with precedence:
  1) `DV_DOCKERFILE` env variable path
  2) `${XDG_CONFIG_HOME}/dv/Dockerfile.local`
  3) Embedded default at `${XDG_CONFIG_HOME}/dv/Dockerfile` with tracked SHA
- `cmd/dv/`: CLI entrypoint
- `internal/cli/`: Subcommand implementations
  - `build.go`: `dv build` builds Docker image
  - `run.go`: `dv run` creates/starts containers with shell/command access
  - `stop.go`: `dv stop` stops containers
  - `cleanup.go`: `dv cleanup` removes containers/images
  - `agent.go`: `dv agent` manages multiple containers
  - `extract.go`: `dv extract` copies container changes to local clone
  - `configcmd.go`: `dv config` manages persisted settings
  - `data.go`: Shows XDG data directory path
  - `completion.go`: `dv completion` manages shell completions
- `internal/config/`: JSON configuration management
- `internal/docker/`: Docker CLI wrappers
- `internal/xdg/`: XDG path helpers
- `bin/`: Legacy bash scripts (superseded by `dv`)

## XDG Paths and Persistence
- Config: `${XDG_CONFIG_HOME}/dv/config.json` (fallback: `~/.config/dv/config.json`)
- Data: `${XDG_DATA_HOME}/dv` (fallback: `~/.local/share/dv`)
- `selectedAgent` in config identifies current container
- `dv extract` uses `${XDG_DATA_HOME}/dv/discourse_src` for local clone
- Dockerfile tracked under `${XDG_CONFIG_HOME}/dv` unless overridden

## Operational Guidelines
- Use `dv` instead of `bin/*` scripts
- Verify image exists (`dv build`) and container runs (`dv run`) before work
- Run container commands with:
  - `dv run -- <command>` (non-interactive)
  - `dv run` (interactive shell)
- Manage containers via `dv agent new|select|list`
- Export changes with `dv extract`, then commit in `${XDG_DATA_HOME}/dv/discourse_src`
- If host port conflicts, use `--host-port` or stop conflicting service

### Go code hygiene
- After editing any Go files, **format code and order imports**:
  ```bash
  gofmt -s -w .
  goimports -w .
  ```
  If `goimports` is not installed:
  ```bash
  go install golang.org/x/tools/cmd/goimports@latest
  ```

## Environment Variables
Auto-passed to container when set on host: `CURSOR_API_KEY`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `CLAUDE_CODE_USE_BEDROCK`, `DEEPSEEK_API_KEY`, `GEMINI_API_KEY`. `CI=1` always set.

Playwright preinstalled with dependencies; no extra setup needed.

## Safety Constraints
- Avoid long-lived background processes; use `dv run` and exit cleanly
- Don't modify `Dockerfile` without explicit instructions
- `dv extract` resets local clone; commit host changes first
- Use caution with `dv cleanup --all` as it removes the image

## Typical Workflows
1. Build and start:
   ```bash
   ./dv build
   ./dv run
   ```

2. Container operations:
   ```bash
   bin/rails c
   bin/rspec
   npx ember test
   ```

3. Export and commit:
   ```bash
   ./dv extract
   cd $(./dv data)/discourse_src
   git status
   git add .
   git commit -m "Describe changes"
   ```

## Extending `dv`
- Add subcommands in `internal/cli/` and wire in `root.go`
- Use `internal/config` for settings with JSON schema compatibility
- Use `internal/docker` helpers for container interactions
- Maintain readable code with clear naming and guard clauses

## Troubleshooting
- Port conflict: use `--host-port` or stop conflicting service
- Missing port mapping: recreate with `dv run --reset`
- No changes detected: verify changes in `/var/www/discourse` and no container commits
- Dockerfile confusion: run `dv build` to see which path was used
