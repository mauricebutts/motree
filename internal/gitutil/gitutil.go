// Package gitutil wraps the git subprocess calls needed for worktree and
// branch management (see docs/superpowers/specs/2026-11-14-motree-go-design.md).
package gitutil

import (
	"fmt"
	"strings"

	"github.com/mauricebutts/motree/internal/executil"
)

// Git wraps the git subprocess calls used for worktree/branch management.
type Git struct {
	Runner executil.Runner
}

// RepoRoot resolves the top-level directory of the git repo containing cwd.
// Returns "" (no error) if cwd is not inside a git repo.
func (g Git) RepoRoot(cwd string) (string, error) {
	stdout, _, err := g.Runner.Run("git", "-C", cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(stdout), nil
}

// DefaultBranch returns the repo's origin/HEAD branch name, falling back to
// "main" if origin/HEAD can't be resolved.
func (g Git) DefaultBranch(repoRoot string) string {
	stdout, _, err := g.Runner.Run("git", "-C", repoRoot, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return "main"
	}
	ref := strings.TrimSpace(stdout)
	branch := strings.TrimPrefix(ref, "refs/remotes/origin/")
	if branch == "" {
		return "main"
	}
	return branch
}

// BranchExists reports whether branch exists as a local branch in repoRoot.
func (g Git) BranchExists(repoRoot, branch string) bool {
	_, _, err := g.Runner.Run("git", "-C", repoRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// FetchOrigin fetches the given branch from origin.
func (g Git) FetchOrigin(repoRoot, branch string) error {
	_, _, err := g.Runner.Run("git", "-C", repoRoot, "fetch", "origin", branch)
	return err
}

// WorktreeAdd creates a worktree at worktreePath checked out to an existing
// branch.
func (g Git) WorktreeAdd(repoRoot, worktreePath, branch string) error {
	_, _, err := g.Runner.Run("git", "-C", repoRoot, "worktree", "add", worktreePath, branch)
	return err
}

// WorktreeAddNewBranch creates a new branch off baseRef in a new worktree at
// worktreePath, then unsets the new branch's upstream tracking (so a stray
// push can't go straight to the base ref). The unset-upstream step is
// best-effort.
func (g Git) WorktreeAddNewBranch(repoRoot, worktreePath, branch, baseRef string) error {
	_, _, err := g.Runner.Run("git", "-C", repoRoot, "worktree", "add", "-b", branch, worktreePath, baseRef)
	if err != nil {
		return err
	}
	g.Runner.Run("git", "-C", worktreePath, "branch", "--unset-upstream")
	return nil
}

// WorktreeAddDetached creates a detached-HEAD worktree at worktreePath.
func (g Git) WorktreeAddDetached(repoRoot, worktreePath string) error {
	_, _, err := g.Runner.Run("git", "-C", repoRoot, "worktree", "add", "--detach", worktreePath)
	return err
}

// WorktreePrune removes stale worktree administrative data.
func (g Git) WorktreePrune(repoRoot string) error {
	_, _, err := g.Runner.Run("git", "-C", repoRoot, "worktree", "prune")
	return err
}

// WorktreeRemove removes the worktree at worktreePath. On failure, the
// returned error's text contains git's actual stderr output, so callers
// that print the error show git's real failure reason.
func (g Git) WorktreeRemove(repoRoot, worktreePath string) error {
	_, stderr, err := g.Runner.Run("git", "-C", repoRoot, "worktree", "remove", worktreePath)
	if err != nil {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git worktree remove %s: %s", worktreePath, msg)
	}
	return nil
}

// BranchDelete deletes the given local branch.
func (g Git) BranchDelete(repoRoot, branch string) error {
	_, _, err := g.Runner.Run("git", "-C", repoRoot, "branch", "-d", branch)
	return err
}
