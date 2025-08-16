# AI Agents Guide for this Repository

Welcome! This guide explains the purpose of the repository, key files, and how autonomous agents should operate safely and productively. The canonical interface is the `dv` Go CLI.

## Repository Purpose
- Provision a Discourse development environment via Docker.
- Provide a single CLI (`dv`) to manage the lifecycle and to export code changes made inside the container to a host-side Git workflow.

## Key Files and Directories
- `internal/assets/Dockerfile`: Embedded base image definition used by the CLI.
- `internal/assets/dockerfile.go`: Embed/resolve logic. Precedence:
  1) `DV_DOCKERFILE` environment variable path
  2) `${XDG_CONFIG_HOME}/dv/Dockerfile.local`
  3) Embedded default materialized to `${XDG_CONFIG_HOME}/dv/Dockerfile` with a tracked SHA
- `cmd/dv/`: Main entrypoint for the `dv` CLI.
- `internal/cli/`: Subcommands implementation.
  - `build.go`: `dv build` builds the Docker image (reports which Dockerfile path is used).
  - `run.go`: `dv run` creates/starts containers and attaches a shell or runs a command; includes host-port preflight check.
  - `stop.go`: `dv stop` stops containers.
  - `cleanup.go`: `dv cleanup` removes containers and (optionally) the image.
  - `agent.go`: `dv agent` manages multiple containers (list/new/select) and persists selection.
  - `extract.go`: `dv extract` copies changed files from the container into a local clone and creates a branch at the container’s HEAD.
  - `configcmd.go`: `dv config get/set/show` to manage persisted settings.
  - `data.go`: prints the XDG data directory path.
  - `completion.go`: `dv completion zsh [--install]` prints or installs zsh completions.
- `internal/config/`: JSON config load/save. Default config seeds image tag, default container, workdir, ports, env passthrough, repo URL, and branch prefix.
- `internal/docker/`: Thin wrappers around `docker` CLI (build/run/exec/start/stop/cp, etc.).
- `internal/xdg/`: Determines XDG config and data directories.
- `bin/`: Legacy bash scripts; kept for reference but superseded by `dv`.

## XDG Paths and Persistence
- Config: `${XDG_CONFIG_HOME}/dv/config.json` (fallback `~/.config/dv/config.json`).
- Data: `${XDG_DATA_HOME}/dv` (fallback `~/.local/share/dv`).
- The `selectedAgent` value in config is the current active container name.
- `dv extract` uses `${XDG_DATA_HOME}/dv/discourse_src` as the local clone.
 - The effective Dockerfile path is materialized and tracked under `${XDG_CONFIG_HOME}/dv` unless overridden.

## Operational Guidance for Agents
- Prefer `dv` over `bin/*` scripts.
- Before starting work: ensure the image exists (`dv build`) and the container is running (`dv run`).
- To run commands inside the container use:
  - `dv run -- <command>` (executes command non-interactively), or
  - `dv run` to open an interactive shell.
- Manage multiple containers with `dv agent new|select|list`.
- Export changes from the container with `dv extract`; then commit on host in `${XDG_DATA_HOME}/dv/discourse_src`.
 - If the host port is busy, `dv run` will refuse to start; choose a different `--host-port` or stop the conflicting service.

## Environment Variables Passed Through
If set on the host, these are passed into the container automatically: `CURSOR_API_KEY`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `CLAUDE_CODE_USE_BEDROCK`, `DEEPSEEK_API_KEY`, `GEMINI_API_KEY`. `CI=1` is always set.
Playwright and its system dependencies are preinstalled in the image; you can run Playwright tests without extra setup.

## Safety & Constraints for Autonomous Agents
- Do not run long-lived background processes via the CLI when unattended; use `dv run` and exit cleanly.
- Avoid modifying `Dockerfile` unless specifically instructed; it encodes required Discourse bootstrapping.
- `dv extract` resets the local clone before copying; ensure you commit host-side changes before running again to avoid losing uncommitted work.
- Be cautious with `dv cleanup --all` as it removes the image.

## Typical Workflows
1. Build and start:
   ```bash
   ./dv build
   ./dv run
   ```
2. Inside the container (interactive):
   ```bash
   bin/rails c
   bin/rspec
   npx ember test
   ```
3. Export changes to host and prepare a PR:
   ```bash
   ./dv extract
   cd $(./dv data)/discourse_src
   git status
   git add .
   git commit -m "Describe your changes"
   ```

## Extending `dv`
- Add new subcommands under `internal/cli/` and wire them in `root.go`.
- Use `internal/config` for persistent settings; maintain JSON schema compatibility.
- Use `internal/docker` helpers for container interactions to keep behavior consistent.
- Keep code readable, with clear naming and guard clauses.

## Troubleshooting
- Port in use error on `dv run`: pass `--host-port` or stop the conflicting service.
- Container missing port mapping: recreate with `dv run --reset`.
- `dv extract` says no changes: ensure you changed files under `/var/www/discourse` and committed nothing inside the container.
 - Dockerfile override confusion: run `dv build` and read the first lines to see which Dockerfile path was used (env override, local override, or embedded).

Happy hacking! Agents should prefer small, reversible steps and confirm success at each stage using the `dv` commands.
