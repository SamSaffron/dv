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

## Installation

### Using the install script (recommended)

Install the latest release for macOS or Linux with a single command:

```bash
curl -sSfL https://raw.githubusercontent.com/SamSaffron/dv/main/install.sh | sh
```

The script downloads the correct binary for your platform and installs it to `~/.local/bin` (create it if missing). After it finishes, run `dv version` to confirm that the binary is on your `PATH`.

To pin a specific release or control the install location:

```bash
# install a specific tag
curl -sSfL https://raw.githubusercontent.com/SamSaffron/dv/main/install.sh | sh -s -- --version v0.3.0

# install without sudo
curl -sSfL https://raw.githubusercontent.com/SamSaffron/dv/main/install.sh | sh -s -- --install-dir ~/.local/bin
```

You can also set the `DV_INSTALL_DIR` environment variable to change the default target directory. If `~/.local/bin` (or your custom path) isn’t on your `PATH`, add it in your shell profile, e.g. `export PATH="$HOME/.local/bin:$PATH"`.

`dv` automatically checks for updates once per day in the background. When a newer release is published you’ll see a warning; run `dv upgrade` to install it in place without re-running the shell script. Update metadata is cached at `${XDG_CONFIG_HOME}/dv/update-state.json`.

### Build from source

If you’re hacking on `dv`, build the binary directly:

```bash
go build
```

The resulting binary is written to the repository root (run it via `./dv`).

## Quick Start

With `dv` installed (either via the script or `go build`), run the CLI directly from your shell. If you’re using the locally built binary in this repository, replace `dv` with `./dv` in the commands below.

1. Build the Docker image:
   ```bash
   dv build
   ```

2. Start the container:
   ```bash
   dv start
   ```
3. Enter the container (interactive shell or run a command):
   ```bash
   dv enter
   # or to run a one-off command
   dv enter -- bin/rails c
   ```
4. Extract changes from the container (when ready to create a PR):
   ```bash
   dv extract
   # or extract changes for a specific plugin (with TAB completion)
   dv extract plugin discourse-akismet
   ```

Optional: manage multiple named containers ("agents"):
```bash
dv new my_project     # create and select a new agent
dv list               # show all agents for the selected image
dv select my_project  # select an existing agent
dv rename old new     # rename an agent
```

## dv Commands

### dv build
Build the Docker image (defaults to tag `ai_agent`).

```bash
dv build [--no-cache] [--build-arg KEY=VAL] [--rm-existing]
```

Notes:
- Uses an embedded `Dockerfile` managed under your XDG config directory. On each build, the CLI ensures the materialized `Dockerfile` matches the embedded version via a SHA file.
- Override precedence:
  1) `DV_DOCKERFILE=/absolute/path/to/Dockerfile`
  2) `${XDG_CONFIG_HOME}/dv/Dockerfile.local`
  3) Embedded default (materialized to `${XDG_CONFIG_HOME}/dv/Dockerfile`)
  The command prints which Dockerfile path it used.
- BuildKit/buildx is enabled by default (`docker buildx build --load`) and stores a persistent cache under `${XDG_DATA_HOME}/dv/buildkit-cache/<image-tag>` so iterative `dv build` runs are much faster. The CLI automatically falls back to legacy `docker build` if buildx is unavailable.
- Opt-out controls: `--classic-build` forces legacy `docker build`, `--no-cache-share` skips the persistent cache, `--cache-dir PATH` pins the cache location, and `--builder NAME` targets a specific buildx builder (remote builders, Docker Build Cloud, etc.).

### dv start
Create or start the container for the selected image (no shell).

```bash
dv start [--reset] [--name NAME] [--image NAME] [--host-starting-port N] [--container-port N]
```

Notes:
- Maps host `4201` → container `4200` by default (Ember CLI dev server). Override with flags.
- Performs a pre-flight check and picks the next free port if needed.

### dv enter
Attach to the running container as user `discourse` in `/var/www/discourse`, or run a one-off command.

```bash
dv enter [--name NAME] [-- cmd ...]
```

Notes:
- Copies any configured host files into the container before launching the shell (see `copyFiles` under config).

### dv run-agent (alias: ra)
Run an AI agent inside the container with a prompt.

```bash
dv run-agent [--name NAME] AGENT [-- ARGS...|PROMPT ...]
# alias
dv ra codex Write a migration to add foo to users

# interactive mode
dv ra codex

# use a file as the prompt (useful for long instructions)
dv ra codex ./prompts/long-instructions.txt
dv ra codex ~/notes/feature-plan.md

# pass raw args directly to the agent (no prompt wrapping)
dv ra aider -- --yes -m "Refactor widget"
```

Notes:
- Autocompletes common agents: `codex`, `aider`, `claude`, `gemini`, `crush`, `cursor`, `opencode`, `amp`.
- If no prompt is provided, an inline TUI opens for multi-line input (Ctrl+D to run, Esc to cancel).
- You can pass a regular file path as the first argument after the agent (e.g. `dv ra codex ./plan.md`). The file will be read on the host and its contents used as the prompt. If the argument is not a file, the existing prompt behavior is used.
- Filename/path completion is supported when you start typing a path (e.g. `./`, `../`, `/`, or include a path separator).
- Agent invocation is rule-based (no runtime discovery). Use `--` to pass raw args unchanged (e.g., `dv ra codex -- --help`).

### dv update agents
Refresh the preinstalled AI agents inside the container (Codex, Gemini, Crush, Claude, Aider, Cursor, OpenCode).

```bash
dv update agents [--name NAME]
```

Notes:
- Starts the container if needed before running updates.
- Re-runs the official install scripts or package managers to pull the latest versions.

### dv stop
Stop the selected or specified container.

```bash
dv stop [--name NAME]
```

### dv remove
Remove the container and optionally the image.

```bash
dv remove [--image] [--name NAME]
```

### Agent management
Manage multiple containers for the selected image; selection is stored in XDG config. These are the preferred top-level commands; the old `dv agent` group has been removed.

```bash
dv list
dv new [NAME]
dv select NAME
dv rename OLD NEW
```

### dv extract
Copy modified files from the running container’s `/var/www/discourse` into a local clone and create a new branch at the container’s HEAD.

```bash
dv extract [--name NAME] [--sync] [--debug]
```

By default, the destination is `${XDG_DATA_HOME}/dv/discourse_src`.

`--sync` keeps the container and host codebases synchronized after the initial extract by watching for changes in both environments (press `Ctrl+C` to exit). `--debug` adds verbose logging while in sync mode. These flags cannot be combined with `--chdir` or `--echo-cd`.

Note: sync mode requires `inotifywait` to be available inside the container (included in latest Dockerfile used here).

Examples:

```bash
# Perform a one-off extract
dv extract

# Start continuous two-way sync with verbose logging
dv extract --sync --debug
```

### dv pr
Checkout a GitHub pull request in the container and reset the development environment.

```bash
dv pr [--name NAME] NUMBER
```

Notes:
- Fetches and checks out the specified PR into a local branch `pr-<NUMBER>`.
- Performs a full database reset and migration (development and test databases).
- Reinstalls dependencies (bundle and pnpm).
- Seeds test users.
- Supports TAB completion with PR numbers and titles from GitHub API.
- Only works with containers using the `discourse` image kind.

Examples:
```bash
# Checkout PR #12345
dv pr 12345

# Use TAB completion to search and select a PR
dv pr <TAB>
```

### dv branch
Checkout a git branch in the container and reset the development environment.

```bash
dv branch [--name NAME] BRANCH
```

Notes:
- Checks out the specified branch and pulls latest changes.
- Performs a full database reset and migration (development and test databases).
- Reinstalls dependencies (bundle and pnpm).
- Seeds test users.
- Supports TAB completion(e.g., `dv branch me<TAB>` queries only branches starting with "me").
- Only works with containers using the `discourse` image kind.

Examples:
```bash
# Checkout main branch
dv branch main

# Use TAB completion to list and select a branch
dv branch <TAB>

# Checkout a feature branch
dv branch feature/my-feature
```

### dv extract plugin
Extract changes for a single plugin from the running container. This is useful when a plugin is its own git repository under `/var/www/discourse/plugins`.

```bash
dv extract plugin <name> [--name NAME] [--chdir] [--echo-cd]
```

Notes:
- Requires the container to be running to discover plugins.
- TAB completion suggests plugin names under `/var/www/discourse/plugins` that are separate git repositories from the core Discourse repo.
- Destination is `${XDG_DATA_HOME}/dv/<PLUGIN>_src`.
- If the plugin is a git repo with a remote, dv clones it and checks out a branch/commit matching the container; only modified/untracked files are copied over.
- If the plugin has no git remote or isn’t a git repo, dv copies the whole directory to `<PLUGIN>_src`.
- `--chdir` opens a subshell in the extracted directory on completion. `--echo-cd` prints a `cd <path>` line to stdout (suitable for `eval`).

Examples:
```bash
# Autocomplete plugin name
dv extract plugin <TAB>

# Extract changes for akismet plugin
dv extract plugin discourse-akismet

# Jump into the extracted repo afterwards
dv extract plugin discourse-akismet --chdir

# Use in command substitution to cd silently
eval "$(dv extract plugin discourse-akismet --echo-cd)"
```

### dv config
Read/write config stored at `${XDG_CONFIG_HOME}/dv/config.json`.

```bash
dv config get KEY
dv config set KEY VALUE
dv config show
```

#### Copying host files on enter
Configure files to copy from the host into the container every time you run `dv enter` by setting `copyFiles` in your config. Keys are host paths (supporting `~` and env vars), values are absolute container paths. A sensible default is provided for Codex auth:

```json
{
  "copyFiles": {
    "~/.codex/auth.json": "/home/discourse/.codex/auth.json"
  }
}
```
The parent directory inside the container is created if needed, and ownership is set to `discourse:discourse` so the file is readable by the working user.

### dv data
Print the data directory path (`${XDG_DATA_HOME}/dv`).

```bash
dv data
```

### dv config completion
Generate shell completion scripts (rarely needed). For zsh:

```bash
dv config completion zsh           # print to stdout
dv config completion zsh --install # install to ~/.local/share/zsh/site-functions/_dv
```

### dv upgrade
Download and replace the current binary with the latest GitHub release (or a specific tag).

```bash
dv upgrade           # install the newest release for your platform
dv upgrade --version v0.3.0
```

The command writes the data to the same path as the running executable, so use `sudo dv upgrade` if `dv` lives somewhere like `/usr/local/bin`.

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
- `AMP_API_KEY`

### Build acceleration toggles

Set these on the host to change how `dv build` (and other build helpers) behave:

- `DV_DISABLE_BUILDX` — force legacy `docker build` even if buildx is available.
- `DV_DISABLE_BUILD_CACHE` — skip the persistent BuildKit cache import/export without changing CLI flags.
- `DV_BUILDX_BUILDER` (or `DV_BUILDER`) — default builder name used for `docker buildx build`, useful for remote builders.
- `DV_BUILDX_CACHE` — override the cache directory used for BuildKit local cache exports.

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
   dv build
   ```
2. Develop inside the container:
   ```bash
   dv start
   dv enter
   # Work with Discourse at /var/www/discourse
   ```
3. Extract changes to a local clone and commit:
   ```bash
   dv extract
   cd $(dv data)/discourse_src
   git add . && git commit -m "Your message"
   ```

## Releases

This project uses automated GitHub releases with cross-platform binary builds for macOS and Linux.

### Creating a Release

1. **Using the release script** (recommended):
   ```bash
   ./scripts/release.sh v1.0.0
   # or automatically bump the patch version based on the latest GitHub release
   ./scripts/release.sh --auto
   ```

2. **Manual process**:
   ```bash
   git tag -a v1.0.0 -m "Release v1.0.0"
   git push origin v1.0.0
   ```

### What Happens Automatically

When you push a tag starting with `v`, GitHub Actions will:

1. **Build binaries** for:
   - Linux (amd64, arm64)
   - macOS (amd64, arm64)

2. **Create a GitHub release** with:
   - Release notes from git commits
   - Binary downloads for each platform
   - Checksums for verification

3. **Archive format**:
   - Linux: `.tar.gz`
   - macOS: `.tar.gz`
   - All platforms include README.md and LICENSE

### Version Information

Check the version of your `dv` binary:
```bash
dv version
```

This will show the version, git commit, and build date.

### Release Configuration

The release process is configured in:
- `.github/workflows/release.yml` - GitHub Actions workflow
- `.goreleaser.yml` - GoReleaser configuration for builds and packaging
