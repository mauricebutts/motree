package mtcmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/mauricebutts/motree/internal/fzfutil"
)

// rmFzfArgs builds `mt rm`'s fzf argv, ported directly from the original
// bash do_rm's fzf invocation:
//
//	fzf --delimiter='\t' --with-nth=2 --height=~40% --reverse --prompt='remove> ' --preview='echo {3}' --preview-window=down,3,wrap
//
// Unlike mt ls's picker (fzfutil.BuildArgs), this never reloads and its
// preview is just the raw description field (column 3), not a __mt_preview
// subprocess call — so it's built here directly rather than through
// BuildArgs.
func rmFzfArgs() []string {
	return []string{
		`--delimiter=\t`,
		"--with-nth=2",
		"--height=~40%",
		"--reverse",
		"--prompt=remove> ",
		"--preview=echo {3}",
		"--preview-window=down,3,wrap",
	}
}

// Rm is the full `mt rm` entrypoint: an fzf picker over tracked sessions
// that, on selection, removes that session entirely (worktrees, branches,
// tmux session, and state). Ported from the original bash motree.sh's
// do_rm.
//
// cwd is accepted for interface symmetry with Create/PR/Spawn/Ls's
// entrypoints; do_rm's own behavior never depended on the invoking
// directory, so Rm doesn't use it.
func Rm(d *Deps, cwd string) error {
	if len(d.State.SortedSessions()) == 0 {
		fmt.Fprintln(os.Stderr, "mt: no sessions tracked yet")
		return nil
	}

	if _, err := fzfLookPath("fzf"); err != nil {
		// Unlike the "no sessions tracked yet" branch above (which prints
		// directly and returns nil, since mt ls's equivalent case has no
		// error to report), this path must surface a non-nil error so the
		// caller exits non-zero — matching the original bash do_rm's
		// `return 1`. main.go's dispatch prints every returned error via
		// "mt: %v", so the message is returned, not also printed here
		// directly: printing it both ways would put the same line on
		// stderr twice.
		return errors.New("mt rm needs fzf installed to pick a session.")
	}

	rows := RenderRows(d.State)

	selectedName, runErr := fzfutil.Run(d.Tmux.Runner, rows, rmFzfArgs())
	if runErr != nil {
		return runErr
	}
	if selectedName == "" {
		return nil
	}

	return RemoveSession(d, selectedName)
}

// RemoveSession removes session name entirely: every repo's worktree, then
// (best-effort) its branch, then its tmux session if one is running, then
// its state row. Ported from the original bash motree.sh's mt_remove.
//
// Worktree removal runs as a first pass over every repo before anything
// else is touched: if ANY repo's worktree fails to remove, the real git
// error text for each failure is printed and RemoveSession aborts, leaving
// state, tmux, and branches untouched — no partial removal.
func RemoveSession(d *Deps, name string) error {
	repos := d.State.ReposFor(name)
	if len(repos) == 0 {
		return fmt.Errorf("mt: nothing recorded for session '%s'", name)
	}

	failed := false
	for _, repo := range repos {
		if err := d.Git.WorktreeRemove(repo.RepoPath, repo.WorktreePath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			failed = true
		}
	}
	if failed {
		return fmt.Errorf("mt: failed to remove worktree(s) for session '%s'", name)
	}

	for _, repo := range repos {
		_ = d.Git.BranchDelete(repo.RepoPath, name) // best-effort, matches bash's "|| true"
	}

	sessionID, err := d.Tmux.FindSessionID(name)
	if err != nil {
		return err
	}
	if sessionID != "" {
		if err := d.Tmux.KillSession(sessionID); err != nil {
			return err
		}
	}

	d.State.RemoveSession(name)
	fmt.Printf("mt: removed session '%s'\n", name)
	return nil
}
