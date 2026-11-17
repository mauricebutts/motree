package mtcmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mauricebutts/motree/internal/tmux"
)

// Spawn creates a brand-new tmux session named name — erroring if one by
// that name already exists — resolves (creating if necessary) the
// worktree/branch for the repo at cwd exactly as Create does, records the
// session/repo in state, builds the session at a fixed 200x50 size with the
// 3-pane layout, starts nvim in the left pane, and sends prompt (quoted via
// tmux.Quote so it survives send-keys unexpanded) into the claude pane. It
// never attaches or switches the tmux client, so several can be spawned in
// a row without taking over the terminal.
//
// Spawn duplicates (rather than sharing a helper for) the worktree/branch
// creation sequence also found in Create.
func Spawn(d *Deps, name, prompt, baseBranchFlag, description, cwd string, now int64) error {
	sessionID, err := d.Tmux.FindSessionID(name)
	if err != nil {
		return err
	}
	if sessionID != "" {
		return fmt.Errorf("mt: session '%s' already exists — pick a different name, or run 'mt %s' to attach", name, name)
	}

	repoRoot, err := d.Git.RepoRoot(cwd)
	if err != nil {
		return err
	}
	if repoRoot == "" {
		return errors.New("motree: not inside a git repo")
	}

	repoName := filepath.Base(repoRoot)
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", name)
	branchName := name

	baseBranch := baseBranchFlag
	if baseBranch == "" {
		baseBranch = d.Git.DefaultBranch(repoRoot)
	}

	if _, statErr := os.Stat(worktreePath); os.IsNotExist(statErr) {
		if err := d.Git.WorktreePrune(repoRoot); err != nil {
			return err
		}
		if err := d.Git.FetchOrigin(repoRoot, baseBranch); err != nil {
			fmt.Fprintln(os.Stderr, "motree: fetch failed, using local refs")
		}

		if d.Git.BranchExists(repoRoot, branchName) {
			if err := d.Git.WorktreeAdd(repoRoot, worktreePath, branchName); err != nil {
				return err
			}
		} else {
			if err := d.Git.WorktreeAddNewBranch(repoRoot, worktreePath, branchName, "origin/"+baseBranch); err != nil {
				return err
			}
		}
	}

	d.State.UpsertSession(name, description, now)
	d.State.EnsureRepo(name, repoRoot, worktreePath)

	newSessionID, err := d.Tmux.NewSession(name, repoName, worktreePath, &tmux.WindowSize{X: 200, Y: 50})
	if err != nil {
		return err
	}

	windowTarget := newSessionID + ":" + repoName

	if err := d.Tmux.SplitThreePane(windowTarget, worktreePath); err != nil {
		return err
	}
	if err := d.Tmux.SendKeys(windowTarget+".0", "nvim ."); err != nil {
		return err
	}
	if err := d.Tmux.SendKeys(windowTarget+".2", "claude "+tmux.Quote(prompt)); err != nil {
		return err
	}

	return nil
}
