package mtcmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/mauricebutts/motree/internal/ghutil"
	"github.com/mauricebutts/motree/internal/tmux"
)

// prNumberPattern validates that a PR argument is digits only.
var prNumberPattern = regexp.MustCompile(`^[0-9]+$`)

// prExplainPrompt is the fixed prompt sent into the claude pane when a PR
// review session's window is newly created. Note the "it's" (not "its")
// — a deliberately preserved typo, not a mistake to "fix".
const prExplainPrompt = "Please give a high level explanation of this PR and it's business purpose"

// PR checks prNumber out into its own session/worktree for review, named
// title when one is given and "pr-<prNumber>" otherwise. The title
// replaces the session name, the worktree directory and the state key
// together; the description stays "PR #<prNumber>" either way, so mt ls
// still shows which PR a renamed session is for.
//
// It requires a numeric prNumber and the gh CLI; if the worktree
// doesn't exist yet, it refuses when some other session already holds
// this PR in this repo (see the guard below), then resolves the PR's head
// branch via gh.PRHeadRef — returning that lookup's error verbatim, with
// no worktree, session, or state row created, if it fails or yields no
// branch — then prunes,
// creates a detached worktree (no base-branch fetch: gh pr checkout
// fetches the PR's own branch), and runs gh.Checkout inside it so gh
// manages the local branch/tracking itself.
//
// Session/window handling follows Create's pattern via ensureSessionWindow,
// except the claude pane (only when the window is newly created) always
// gets the fixed prExplainPrompt rather than claudecmd.ResumeCommand, and —
// unlike Spawn — PR always attaches at the end.
func PR(d *Deps, gh ghutil.GH, prNumber, title, cwd string, insideTmux bool, now int64) error {
	if !prNumberPattern.MatchString(prNumber) {
		return errors.New("mt: pr requires a numeric PR number")
	}

	if !ghutil.Available() {
		return errors.New("mt: pr requires the GitHub CLI (gh) to be installed")
	}

	repoRoot, err := d.Git.RepoRoot(cwd)
	if err != nil {
		return err
	}
	if repoRoot == "" {
		return errors.New("motree: not inside a git repo")
	}

	repoName := filepath.Base(repoRoot)
	name := title
	if name == "" {
		name = "pr-" + prNumber
	}
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", name)

	if _, statErr := os.Stat(worktreePath); os.IsNotExist(statErr) {
		// Another session already holding this PR means a second worktree
		// for the same branch, which git refuses halfway through with
		// "already checked out at ..." from inside gh pr checkout. Refuse
		// up front instead, naming the session that has it. Sitting behind
		// the os.Stat above keeps re-running the same invocation
		// idempotent; the name comparison covers the case where state
		// still holds the tie but the worktree was removed by hand.
		if existing := d.State.SessionForPR(repoRoot, prNumber); existing != "" && existing != name {
			return fmt.Errorf("PR %s is already checked out as session '%s'", prNumber, existing)
		}

		// gh's own stderr is the only thing that tells a missing PR
		// apart from a repo gh can't see (wrong account, SSO not
		// authorized) — both exit 1 — so it goes straight back to the
		// caller rather than being flattened into one mt-authored line.
		headRef, err := gh.PRHeadRef(repoRoot, prNumber)
		if err != nil {
			return err
		}
		if headRef == "" {
			return fmt.Errorf("gh reported no head branch for PR %s", prNumber)
		}

		if err := d.Git.WorktreePrune(repoRoot); err != nil {
			return err
		}
		if err := d.Git.WorktreeAddDetached(repoRoot, worktreePath); err != nil {
			return err
		}
		if err := gh.Checkout(worktreePath, prNumber); err != nil {
			return err
		}
	}

	d.State.UpsertSession(name, "PR #"+prNumber, now)
	d.State.EnsureRepoPR(name, repoRoot, worktreePath, prNumber)

	sessionID, windowTarget, isNewWindow, err := ensureSessionWindow(d, name, repoName, worktreePath, nil)
	if err != nil {
		return err
	}

	if isNewWindow {
		if err := d.Tmux.SendKeys(windowTarget+".2", "claude "+tmux.Quote(prExplainPrompt)); err != nil {
			return err
		}
	}

	return saveAndAttach(d, sessionID, insideTmux)
}
