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

// ErrNoPRFound is the sentinel PR returns after it has already printed
// "no pr found" to stdout itself. It exists for the same reason
// cmd/mt/main.go's own errUsageShown does: main's dispatch prints every
// other returned error via "mt: %v", which would put a second line on the
// screen for this one event. main checks for this sentinel and exits 1
// silently instead.
var ErrNoPRFound = errors.New("no pr found")

// PR checks prNumber out into its own "pr-<prNumber>" session/worktree for
// review. It requires a numeric prNumber and the gh CLI; if the worktree
// doesn't exist yet, it resolves the PR's head branch via gh.PRHeadRef —
// printing "no pr found" and returning an error, with no worktree,
// session, or state row created, if that comes back empty — then prunes,
// creates a detached worktree (no base-branch fetch: gh pr checkout
// fetches the PR's own branch), and runs gh.Checkout inside it so gh
// manages the local branch/tracking itself.
//
// Session/window handling follows Create's pattern via ensureSessionWindow,
// except the claude pane (only when the window is newly created) always
// gets the fixed prExplainPrompt rather than claudecmd.ResumeCommand, and —
// unlike Spawn — PR always attaches at the end.
func PR(d *Deps, gh ghutil.GH, prNumber, cwd string, insideTmux bool, now int64) error {
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
	name := "pr-" + prNumber
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", name)

	if _, statErr := os.Stat(worktreePath); os.IsNotExist(statErr) {
		headRef, err := gh.PRHeadRef(repoRoot, prNumber)
		if err != nil {
			return err
		}
		if headRef == "" {
			fmt.Println("no pr found")
			return ErrNoPRFound
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
	d.State.EnsureRepo(name, repoRoot, worktreePath)

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
