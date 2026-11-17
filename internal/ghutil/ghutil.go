// Package ghutil wraps the gh (GitHub CLI) subprocess calls needed for PR
// worktree checkout (see docs/superpowers/specs/2026-11-14-motree-go-design.md).
package ghutil

import (
	"os/exec"
	"strings"

	"github.com/mauricebutts/motree/internal/executil"
	"github.com/mauricebutts/motree/internal/tmux"
)

// GH wraps the gh subprocess calls used by `mt pr`.
type GH struct {
	Runner executil.Runner
}

// LookPath resolves name to an executable's path, exactly like
// exec.LookPath (its default value). It's a package-level var rather than
// a direct exec.LookPath call so Available() is deterministically
// testable: tests (in this package and in callers like mtcmd) can
// reassign it to force the "gh is/isn't on PATH" result, instead of
// Available()'s result silently depending on whether gh happens to be
// installed on whatever machine runs `go test`. Reassign the original
// value back (e.g. via t.Cleanup) after overriding it.
var LookPath = exec.LookPath

// Available reports whether the gh CLI is present on PATH.
func Available() bool {
	_, err := LookPath("gh")
	return err == nil
}

// PRHeadRef resolves prNumber's head branch name, running
// "gh pr view <prNumber> --json headRefName --jq .headRefName" from
// repoRoot. It returns "" (no error) on any failure or empty output —
// callers can't distinguish "PR not found" from "gh/network failure", by
// design.
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

// runIn runs name/args as if dir were the process's working directory.
// Unlike gitutil (git's own "-C <dir>" flag) and tmux (its own "-c <dir>"
// flag on new-session/new-window/split-window), gh has no generic
// per-invocation flag for "run as if cwd were <dir>" — "gh pr view" and
// "gh pr checkout" both just read the process's actual working directory
// to figure out which repo/PR they're operating on. executil.Runner.Run
// itself has no cwd parameter either (its signature is shared verbatim by
// gitutil/tmux/ghutil, so it isn't ghutil's to change). So runIn
// wraps the command in a "cd <dir> && ..." one-liner run via "sh -c" —
// each argument POSIX-single-quote-escaped (via tmux.Quote, the same
// escaping tmux's own send-keys commands rely on) so it survives
// unexpanded regardless of spaces or shell metacharacters in dir/args.
func (g GH) runIn(dir, name string, args ...string) (string, error) {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, tmux.Quote(name))
	for _, a := range args {
		parts = append(parts, tmux.Quote(a))
	}
	script := "cd " + tmux.Quote(dir) + " && " + strings.Join(parts, " ")

	stdout, _, err := g.Runner.Run("sh", "-c", script)
	return stdout, err
}
