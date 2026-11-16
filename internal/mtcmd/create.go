package mtcmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mauricebutts/motree/internal/claudecmd"
	"github.com/mauricebutts/motree/internal/tmux"
)

// Create creates (if missing) or expands the named session: resolves the
// worktree/branch for the repo at cwd, records the session/repo in state,
// ensures a tmux session/window exists for it (via ensureSessionWindow,
// which only splits the 3-pane layout and starts nvim when the window is
// newly created), and — only in that newly-created case — starts claude in
// the right-hand pane before attaching/switching to the session.
//
// This mirrors the original bash motree.sh's bottom "main" flow (the plain
// `mt <name>` path, as opposed to `spawn`/`pr`).
func Create(d *Deps, name, baseBranchFlag, description, cwd string, insideTmux bool, now int64) error {
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

	sessionID, windowTarget, isNewWindow, err := ensureSessionWindow(d, name, repoName, worktreePath, nil)
	if err != nil {
		return err
	}

	if isNewWindow {
		if err := d.Tmux.SendKeys(windowTarget+".2", claudecmd.ResumeCommand(worktreePath)); err != nil {
			return err
		}
	}

	return d.Tmux.AttachOrSwitch(sessionID, insideTmux)
}

// ensureSessionWindow finds or creates the tmux session sessionName, finds
// or creates a window named repoName in it, and — only when that window is
// newly created — splits the 3-pane layout and sends "nvim ." into pane 0.
//
// It does not send anything into the claude pane, and does not attach:
// callers do both themselves, since each caller needs a different
// command/prompt in the claude pane and a different attach policy (Create
// and AttachOrRecreate only attach for the picker/CLI's own end, while pr
// always attaches). size is nil for Create/PR (tmux picks its own default
// session size); Spawn (a brand-new session/window every time) doesn't use
// this helper at all.
//
// Shared within package mtcmd by Create (this file), pr (Task 11), and ls's
// AttachOrRecreate (Task 13) — keep this signature stable.
func ensureSessionWindow(d *Deps, sessionName, repoName, cwd string, size *tmux.WindowSize) (sessionID, windowTarget string, isNewWindow bool, err error) {
	sessionID, err = d.Tmux.FindSessionID(sessionName)
	if err != nil {
		return "", "", false, err
	}

	if sessionID == "" {
		sessionID, err = d.Tmux.NewSession(sessionName, repoName, cwd, size)
		if err != nil {
			return "", "", false, err
		}
		isNewWindow = true
	} else {
		exists, werr := d.Tmux.WindowExists(sessionID, repoName)
		if werr != nil {
			return "", "", false, werr
		}
		if exists {
			isNewWindow = false
		} else {
			if werr := d.Tmux.NewWindow(sessionID, repoName, cwd); werr != nil {
				return "", "", false, werr
			}
			isNewWindow = true
		}
	}

	windowTarget = sessionID + ":" + repoName

	if isNewWindow {
		if err = d.Tmux.SplitThreePane(windowTarget, cwd); err != nil {
			return "", "", false, err
		}
		if err = d.Tmux.SendKeys(windowTarget+".0", "nvim ."); err != nil {
			return "", "", false, err
		}
	}

	return sessionID, windowTarget, isNewWindow, nil
}
