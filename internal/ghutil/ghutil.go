// Package ghutil wraps the gh (GitHub CLI) subprocess calls needed for PR
// worktree checkout, mirroring the original bash motree tool's do_pr gh
// usage (see docs/superpowers/specs/2026-11-14-motree-go-design.md).
package ghutil

import (
	"os/exec"
	"strings"

	"github.com/mauricebutts/motree/internal/executil"
)

// GH wraps the gh subprocess calls used by `mt pr`.
type GH struct {
	Runner executil.Runner
}

// Available reports whether the gh CLI is present on PATH.
func Available() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// PRHeadRef resolves prNumber's head branch name, running
// "gh pr view <prNumber> --json headRefName --jq .headRefName" from
// repoRoot. It returns "" (no error) on any failure or empty output,
// mirroring the original bash tool's "... 2>/dev/null || true" pattern —
// callers can't distinguish "PR not found" from "gh/network failure", by
// design, same as the bash version.
func (g GH) PRHeadRef(repoRoot, prNumber string) (string, error) {
	stdout, err := g.runIn(repoRoot, "gh", "pr", "view", prNumber, "--json", "headRefName", "--jq", ".headRefName")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(stdout), nil
}

// Checkout runs "gh pr checkout <prNumber>" with worktreePath as its
// working directory, so gh manages that worktree's local branch and its
// tracking itself.
func (g GH) Checkout(worktreePath, prNumber string) error {
	_, err := g.runIn(worktreePath, "gh", "pr", "checkout", prNumber)
	return err
}

// runIn runs name/args as if dir were the process's working directory,
// via a "cd <dir> && ..." shell one-liner — the same subshell approach the
// original bash tool uses ("(cd "$dir" && gh ...)") — since
// executil.Runner.Run has no cwd parameter of its own.
func (g GH) runIn(dir, name string, args ...string) (string, error) {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quote(name))
	for _, a := range args {
		parts = append(parts, quote(a))
	}
	script := "cd " + quote(dir) + " && " + strings.Join(parts, " ")

	stdout, _, err := g.Runner.Run("sh", "-c", script)
	return stdout, err
}

// quote applies POSIX single-quote escaping so s survives unexpanded in
// the "sh -c" one-liner built by runIn.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
