# AI Agents Guide
<!-- Reminder: keep this document updated whenever CLI behavior changes. -->

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
  - `start.go`: `dv start` creates or starts containers (no shell)
  - `enter.go`: `dv enter` opens an interactive shell inside the container
  - `run.go`: `dv run` executes commands inside the container without opening a shell
  - `stop.go`: `dv stop` stops containers
- `remove.go`: `dv remove` removes containers/images
  - `agent.go`: implements top-level `list`, `new`, `select`, `rename` agent commands
  - `extract.go`: `dv extract` copies container changes to local clone
  - `configcmd.go`: `dv config` manages persisted settings
  - `data.go`: Shows XDG data directory path
- `completion.go`: `dv config completion` manages shell completions
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
- Verify image exists (`dv build`) and container is started (`dv start`) before work
- Run container commands with:
  - `dv run -- <command>` (non-interactive)
  - `dv enter` (interactive shell)
- Launch preconfigured CLIs inside the container with `dv run-agent` / `dv ra`
- Manage containers via `dv new|select|list|rename`
- Export changes with `dv extract`, then commit in `${XDG_DATA_HOME}/dv/discourse_src`
- Use `dv extract --sync` (optionally `--debug`) to keep the host and container code trees synchronized in real time; press `Ctrl+C` to stop sync mode
- Ensure the container has `inotifywait` available (install the `inotify-tools` package or equivalent) before starting sync mode
- If host port conflicts, use `--host-port` or stop conflicting service

## Command Reference

### Container & Image Lifecycle
- `dv build` — Build the selected image using the embedded Dockerfile (honors overrides/env flags).
- `dv pull [NAME]` — Pull a published image/tag (optionally retag) instead of building locally.
- `dv image list|show|select|add|set|rename|remove` — Manage image definitions, workdirs, ports, and Dockerfile sources.
- `dv start [--reset]` — Create/start the selected container (auto-picks host ports).
- `dv stop`, `dv restart`, `dv restart discourse` — Stop containers, restart them fully, or restart just runit services.
- `dv remove [NAME] [--image]` — Delete containers (optionally the backing image too).
- `dv expose [--port]` — Proxy the container onto every LAN interface for device testing; Ctrl+C to stop.
- `dv tui` — TUI for selecting agents, running commands, and inspecting status.

### Agent Containers (per-image instances)
- `dv list` — Show containers tied to the selected image (marks the active one).
- `dv new [NAME]` — Create a new container and select it.
- `dv select NAME` — Switch the active container.
- `dv rename OLD NEW` — Rename a container (updates config metadata).
- `dv data` — Print `${XDG_DATA_HOME}/dv` so scripts can locate extracts.

### Working Inside the Container
- `dv run -- <command>` — Non-interactive execution (supports `--root`).
- `dv enter` — Interactive login shell (copyFiles synced beforehand).
- `dv run-agent/ra` — Run supported AI tooling with prompt/arg passthrough.

### Code Sync & Git Integration
- `dv extract [--sync|--debug|--chdir|--echo-cd]` — Copy the container workspace into `${XDG_DATA_HOME}/dv/discourse_src`, optionally keep it bidirectionally synced or emit a `cd` command.
- `dv extract plugin <name>` — Mirror a plugin repo beneath `${XDG_DATA_HOME}/dv/<plugin>_src`, respecting Git remotes.
- `dv import [--base main]` — Push local commits/uncommitted work from the host repo into the running container.
- `dv branch BRANCH`, `dv pr NUMBER` — Checkout upstream branches or GitHub PRs inside the container and rerun migrations/seed steps.

### Configuration, Metadata & Tooling
- `dv config show|get|set KEY VALUE` — Manage JSON config (`selectedAgent`, env passthrough, copyFiles, etc.).
- `dv config completion <shell>` — Install shell completions.
- `dv config ccr` — Bootstrap Claude Code Router presets via OpenRouter/OpenAI rankings.
- `dv config mcp NAME` — Configure Playwright/Discourse MCP servers inside the container (writes TOML, sets envs).

### Updates & Diagnostics
- `dv update agents` — Refresh bundled AI tools inside the running container.
- `dv update discourse [--image NAME]` — Rebuild the Discourse base image using the embedded updater Dockerfile.
- `dv version` — Print the running CLI version (and update warnings).
- `dv upgrade [--version vX.Y.Z]` — Download and replace the current binary in-place.
- `dv update-check` — Hidden command invoked automatically to populate `${XDG_CONFIG_HOME}/dv/update-state.json`.

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
Auto-passed to container when set on host: `CURSOR_API_KEY`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `CLAUDE_CODE_USE_BEDROCK`, `DEEPSEEK_API_KEY`, `GEMINI_API_KEY`, `AMP_API_KEY`.

Playwright preinstalled with dependencies; no extra setup needed.

## Safety Constraints
- Avoid long-lived background processes; use `dv enter` and exit cleanly
- Don't modify `Dockerfile` without explicit instructions
- `dv extract` resets local clone; commit host changes first
- Use caution with `dv remove --image` as it removes the image
- Do not commit changes from the AI assistant. The human maintainer will review and commit. When proposing edits, apply them locally without creating git commits.

## Typical Workflows
1. Build and start:
   ```bash
   ./dv build
   ./dv start
   ./dv enter
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
- Missing port mapping: recreate with `dv start --reset`
- No changes detected: verify changes in `/var/www/discourse` and no container commits
- Dockerfile confusion: run `dv build` to see which path was used
