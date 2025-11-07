package cli

import (
	"fmt"
	"strings"
)

// buildDiscourseResetScript generates a shell script that performs common
// Discourse development environment reset tasks:
// - Stops services (unicorn, ember-cli)
// - Cleans working tree
// - Ensures full git history
// - Executes custom checkout commands
// - Reinstalls dependencies
// - Resets and migrates databases
// - Seeds users
// - Restarts services on exit
//
// checkoutCmds should contain the git checkout logic specific to the caller
// (e.g., PR checkout, branch checkout).
func buildDiscourseResetScript(checkoutCmds []string) string {
	lines := []string{
		"set -euo pipefail",
		"cleanup() { echo 'Starting services (as root): unicorn and ember-cli'; sudo /usr/bin/sv start unicorn || sudo sv start unicorn || true; sudo /usr/bin/sv start ember-cli || sudo sv start ember-cli || true; }",
		"trap cleanup EXIT",
		"echo 'Cleaning working tree...'",
		"git reset --hard",
		"git clean -fd",
		"echo 'Ensuring full history is available (unshallow if needed)...'",
		"if [ -f .git/shallow ]; then git fetch origin --tags --prune --unshallow; else git fetch origin --tags --prune; fi",
	}

	// Insert caller-specific checkout commands
	lines = append(lines, checkoutCmds...)

	// Common post-checkout steps
	lines = append(lines,
		"echo 'Current HEAD:'",
		"git --no-pager log --oneline -n 1",
		"echo 'Reinstalling dependencies (bundle and pnpm) if needed...'",
		// Best-effort; do not fail the whole command if these fail
		"(bundle check || bundle install) || true",
		"(command -v pnpm >/dev/null 2>&1 && pnpm install) || true",
		"echo 'Stopping services (as root): unicorn and ember-cli'",
		"sudo -n true 2>/dev/null || true",
		"sudo /usr/bin/sv stop unicorn || sudo sv stop unicorn || true",
		"sudo /usr/bin/sv stop ember-cli || sudo sv stop ember-cli || true",
		"echo 'Resetting and migrating databases (development and test)...'",
		"MIG_LOG_DEV=/tmp/dv-migrate-dev-$(date +%s).log",
		"MIG_LOG_TEST=/tmp/dv-migrate-test-$(date +%s).log",
		"(bin/rake db:drop || true)",
		"bin/rake db:create",
		"echo \"Migrating dev DB (output -> $MIG_LOG_DEV)\"",
		"bin/rake db:migrate > \"$MIG_LOG_DEV\" 2>&1",
		"echo \"Migrating test DB (output -> $MIG_LOG_TEST)\"",
		"RAILS_ENV=test bin/rake db:migrate > \"$MIG_LOG_TEST\" 2>&1",
		"bundle",
		"pnpm install",
		"echo 'Seeding users...'",
		"bin/rails r /tmp/seed_users.rb || true",
		"echo 'Migration logs:'",
		"echo \"  dev : $MIG_LOG_DEV\"",
		"echo \"  test: $MIG_LOG_TEST\"",
		"echo 'Done.'",
	)

	return strings.Join(lines, "\n")
}

// buildPRCheckoutCommands generates git commands to fetch and checkout a PR.
// It uses the actual branch name from GitHub to maintain branch identity.
func buildPRCheckoutCommands(prNumber int, branchName string) []string {
	return []string{
		fmt.Sprintf("echo 'Fetching PR #%d (branch: %s) from origin...'", prNumber, branchName),
		fmt.Sprintf("git fetch origin pull/%d/head", prNumber),
		fmt.Sprintf("git checkout -B %s FETCH_HEAD", branchName),
	}
}

// buildBranchCheckoutCommands generates git commands to checkout a branch.
func buildBranchCheckoutCommands(branchName string) []string {
	return []string{
		fmt.Sprintf("echo 'Checking out branch %s...'", branchName),
		fmt.Sprintf("git checkout %s", branchName),
		"git pull --ff-only || true",
	}
}
