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
- `main.go`: CLI entrypoint
- `internal/cli/`: Subcommand implementations
  - `build.go`: `dv build` builds Docker image
  - `start.go`: `dv start` creates or starts containers (no shell)
  - `enter.go`: `dv enter` opens an interactive shell inside the container
  - `run.go`: `dv run` executes commands inside the container without opening a shell
  - `copy.go`: `dv copy` (alias `cp`) copies files between host and container
  - `stop.go`: `dv stop` stops containers
  - `restart.go`: `dv restart` / `dv restart discourse` restarts containers or services
  - `remove.go`: `dv remove` removes containers/images
  - `list.go`, `new.go`, `select.go`, `rename.go`: top-level agent management
  - `extract.go`: `dv extract` copies container changes to local clone
  - `reset.go`: `dv reset` resets Discourse databases
  - `mail.go`: `dv mail` runs MailHog with a local tunnel
  - `expose.go`: `dv expose` proxies the container to LAN
  - `tui.go`: `dv tui` opens the management TUI
  - `configcmd.go`: `dv config` manages persisted settings
  - `data.go`: `dv data` shows XDG data directory path
  - `completion.go`: `dv config completion` manages shell completions
- `internal/config/`: JSON configuration management
- `internal/docker/`: Docker CLI wrappers
- `internal/xdg/`: XDG path helpers

## XDG Paths and Persistence
- Config: `${XDG_CONFIG_HOME}/dv/config.json` (fallback: `~/.config/dv/config.json`)
- Data: `${XDG_DATA_HOME}/dv` (fallback: `~/.local/share/dv`)
- `selectedAgent` in config identifies current container
- `dv extract` writes to `${XDG_DATA_HOME}/dv/discourse_src` by default; when a container overrides its workdir, the destination becomes `${XDG_DATA_HOME}/dv/<workdir-slug>_src`.
- Dockerfile tracked under `${XDG_CONFIG_HOME}/dv` unless overridden

## Operational Guidelines
- Use `dv` instead of `bin/*` scripts
- Verify image exists (`dv build`) and container is started (`dv start`) before work
- Run container commands with:
  - `dv run -- <command>` (non-interactive)
  - `dv enter` (interactive shell)
- Launch preconfigured CLIs inside the container with `dv run-agent` / `dv ra`
- Manage containers via `dv new|select|list|rename`
- Export changes with `dv extract`, then commit in `${XDG_DATA_HOME}/dv/discourse_src` (or the slugged custom path when using `dv config workdir` or `dv config theme`)
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
- `dv reset` — Stop Discourse services, reset databases, run migrations and seeds, and restart services.
- `dv remove [NAME] [--image]` — Delete containers (optionally the backing image too).
- `dv expose [--port]` — Proxy the container onto every LAN interface for device testing; Ctrl+C to stop.
- `dv mail [--port PORT] [--host-port HOST_PORT]` — Start MailHog in the container and tunnel it to localhost.
- `dv tui` — TUI for selecting agents, running commands, and inspecting status.

### Agent Containers (per-image instances)
- `dv list` — Show containers tied to the selected image (marks the active one).
- `dv new [NAME] [--template T]` — Create a new container (optionally from a template) and select it.
- `dv select NAME` — Switch the active container for this terminal session and set as default for new terminals. Each terminal maintains its own selection (stored in `$XDG_RUNTIME_DIR`).
- `dv rename OLD NEW` — Rename a container (updates config metadata).
- `dv data` — Print `${XDG_DATA_HOME}/dv` so scripts can locate extracts.

### Working Inside the Container
- `dv run -- <command>` — Non-interactive execution (supports `--root`).
- `dv enter` — Interactive login shell (copyFiles synced beforehand).
- `dv run-agent/ra` — Run supported AI tooling with prompt/arg passthrough.
- `dv config workdir [PATH|--reset]` — Override (or clear) the selected container’s workdir (use `--container` to target another agent).
- `dv copy/cp SOURCE DEST` — Copy files between host and container (supports `@:path` for selected agent).

### Code Sync & Git Integration
- `dv extract [--sync|--debug|--chdir|--echo-cd]` — Copy the active workspace into `${XDG_DATA_HOME}/dv/discourse_src` (default workdir) or `${XDG_DATA_HOME}/dv/<workdir-slug>_src` for custom workdirs, optionally keep it bidirectionally synced or emit a `cd` command. With `--sync`, file changes are synced bidirectionally and git state (commits, branches) is automatically synced from host to container via git bundle.
- `dv extract plugin <name>` — Mirror a plugin repo beneath `${XDG_DATA_HOME}/dv/<plugin>_src`, respecting Git remotes.
- `dv import [--base main]` — Push local commits/uncommitted work from the host repo into the running container.
- `dv branch BRANCH`, `dv pr NUMBER` — Checkout upstream branches or GitHub PRs inside the container and rerun migrations/seed steps.

### Configuration, Metadata & Tooling
- `dv config show|get|set KEY VALUE` — Manage JSON config (`selectedAgent`, env passthrough, copyFiles, etc.).
- `dv config workdir [PATH|--reset]` — Set a per-container entrypoint directory for interactive commands (pass `--container` to pick a different agent).
- `dv config theme [REPO]` — Scaffold or clone a theme under `/home/discourse`, prompt for theme vs component, install `discourse_theme`, write an `AGENTS.md` brief, configure a `theme-watch-<slug>` runit service (backed by a generated admin-bound API key), and point the workdir at the new directory.
- `dv config ai` — Launch TUI to configure Discourse AI LLM providers and models.
- `dv config ai-tool [NAME]` — Scaffold a new AI tool workspace with test and sync helpers.
- `dv config site_settings FILENAME` — Apply site settings from a YAML file (supports 1Password `op://` refs).
- `dv config completion <shell>` — Install shell completions.
- `dv config ccr` — Bootstrap Claude Code Router presets via OpenRouter/OpenAI rankings.
- `dv config mcp NAME` — Configure Playwright/Discourse MCP servers inside the container (writes TOML, sets envs).
- `dv config local-proxy` — Build/start a lightweight proxy container (defaults: `dv-local-proxy` on ports 80/2080; add `--https` for 443) so new agents are reachable at `NAME.dv.localhost`; by default binds to localhost only, use `--public` to expose on all network interfaces. The container automatically restarts on boot. Use `--remove` to stop and remove the proxy container and image.

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

- After running `go build` leave the dv binary around. No need to delete it.

## Environment Variables
Auto-passed to container when set on host: `CURSOR_API_KEY`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `CLAUDE_CODE_USE_BEDROCK`, `DEEPSEEK_API_KEY`, `GEMINI_API_KEY`, `AMP_API_KEY`, `FACTORY_API_KEY`, `MISTRAL_API_KEY`.

Host-side environment variables:
- `DV_AGENT`: Override the selected container for the current process (takes priority over session and global config).

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
