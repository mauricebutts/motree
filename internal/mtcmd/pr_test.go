package mtcmd

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mauricebutts/motree/internal/executil"
	"github.com/mauricebutts/motree/internal/ghutil"
)

// forceGhAvailable overrides ghutil.LookPath so ghutil.Available() returns
// true for the duration of the test, deterministically — independent of
// whether gh actually happens to be installed on whatever machine runs
// `go test`.
func forceGhAvailable(t *testing.T) {
	t.Helper()
	orig := ghutil.LookPath
	ghutil.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	t.Cleanup(func() { ghutil.LookPath = orig })
}

// forceGhUnavailable overrides ghutil.LookPath so ghutil.Available()
// returns false for the duration of the test, deterministically.
func forceGhUnavailable(t *testing.T) {
	t.Helper()
	orig := ghutil.LookPath
	ghutil.LookPath = func(file string) (string, error) { return "", errors.New("executable file not found in $PATH") }
	t.Cleanup(func() { ghutil.LookPath = orig })
}

// captureStdout runs fn with os.Stdout redirected, returning whatever it
// wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return string(out)
}

func TestPR_NonNumericArg_ReturnsError(t *testing.T) {
	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{}
	ghRunner := &executil.FakeRunner{}
	d := newDeps(gitRunner, tmuxRunner)
	gh := ghutil.GH{Runner: ghRunner}

	err := PR(d, gh, "12abc", "/tmp/whatever", false, 1)
	if err == nil {
		t.Fatalf("PR returned nil error, want an error — PR number must be numeric only")
	}

	if len(gitRunner.Calls) != 0 || len(tmuxRunner.Calls) != 0 || len(ghRunner.Calls) != 0 {
		t.Errorf("Calls: git=%+v tmux=%+v gh=%+v, want none", gitRunner.Calls, tmuxRunner.Calls, ghRunner.Calls)
	}
}

func TestPR_GhNotAvailable_ReturnsError(t *testing.T) {
	forceGhUnavailable(t)

	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{}
	ghRunner := &executil.FakeRunner{}
	d := newDeps(gitRunner, tmuxRunner)
	gh := ghutil.GH{Runner: ghRunner}

	err := PR(d, gh, "42", "/tmp/whatever", false, 1)
	if err == nil {
		t.Fatalf("PR returned nil error, want an error — gh is not on PATH")
	}

	if len(gitRunner.Calls) != 0 || len(tmuxRunner.Calls) != 0 || len(ghRunner.Calls) != 0 {
		t.Errorf("Calls: git=%+v tmux=%+v gh=%+v, want none", gitRunner.Calls, tmuxRunner.Calls, ghRunner.Calls)
	}
}

func TestPR_NotInsideGitRepo_ReturnsExactErrorText(t *testing.T) {
	forceGhAvailable(t)
	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("not a git repo")},
		},
	}
	tmuxRunner := &executil.FakeRunner{}
	ghRunner := &executil.FakeRunner{}
	d := newDeps(gitRunner, tmuxRunner)
	gh := ghutil.GH{Runner: ghRunner}

	err := PR(d, gh, "42", "/tmp/not-a-repo", false, 1)
	if err == nil {
		t.Fatalf("PR returned nil error, want %q", "motree: not inside a git repo")
	}
	if err.Error() != "motree: not inside a git repo" {
		t.Errorf("PR error = %q, want %q", err.Error(), "motree: not inside a git repo")
	}

	if len(ghRunner.Calls) != 0 {
		t.Errorf("gh Calls = %+v, want none", ghRunner.Calls)
	}
	if len(d.State.Sessions) != 0 {
		t.Errorf("State.Sessions = %+v, want none", d.State.Sessions)
	}
}

// assertPRLookupOnly checks that PR stopped right after the gh lookup:
// nothing printed to stdout, no worktree prune/add, no gh checkout, no
// tmux, no state row, and no directory left on disk.
func assertPRLookupOnly(t *testing.T, out string, d *Deps, gitRunner, tmuxRunner, ghRunner *executil.FakeRunner, repoRoot, prNumber string) {
	t.Helper()

	if out != "" {
		t.Errorf("stdout = %q, want nothing — the failure belongs on stderr via main's \"mt: %%v\"", out)
	}
	if len(ghRunner.Calls) != 1 {
		t.Errorf("gh Calls = %+v, want exactly 1 (the PRHeadRef lookup)", ghRunner.Calls)
	}
	wantGitCalls := []executil.Call{
		{Name: "git", Args: []string{"-C", repoRoot, "rev-parse", "--show-toplevel"}},
	}
	if !reflect.DeepEqual(gitRunner.Calls, wantGitCalls) {
		t.Errorf("git Calls = %+v, want %+v (no worktree prune/add)", gitRunner.Calls, wantGitCalls)
	}
	if len(tmuxRunner.Calls) != 0 {
		t.Errorf("tmux Calls = %+v, want none", tmuxRunner.Calls)
	}
	if len(d.State.Sessions) != 0 {
		t.Errorf("State.Sessions = %+v, want none", d.State.Sessions)
	}
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", "pr-"+prNumber)
	if _, statErr := os.Stat(worktreePath); !os.IsNotExist(statErr) {
		t.Errorf("worktreePath %q unexpectedly exists", worktreePath)
	}
}

// A failing gh lookup must reach the user verbatim. gh reports a missing
// PR and a repo it can't see (wrong account, un-authorized SSO) with the
// same exit code and only different stderr, so mt printing its own
// blanket "no pr found" actively misdiagnosed the second case.
func TestPR_GhLookupFails_SurfacesGhStderrAndCreatesNothing(t *testing.T) {
	forceGhAvailable(t)
	repoRoot := t.TempDir()
	prNumber := "420"

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"}, // rev-parse --show-toplevel
		},
	}
	tmuxRunner := &executil.FakeRunner{}
	ghRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{
				Stderr: "GraphQL: Could not resolve to a Repository with the name 'acme/widgets'. (repository)\n",
				Err:    errors.New("exit status 1"),
			},
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	gh := ghutil.GH{Runner: ghRunner}

	var err error
	out := captureStdout(t, func() {
		err = PR(d, gh, prNumber, repoRoot, false, 1)
	})

	want := "gh pr view 420 failed: GraphQL: Could not resolve to a Repository with the name 'acme/widgets'. (repository)"
	if err == nil || err.Error() != want {
		t.Fatalf("PR error = %v, want %q", err, want)
	}
	assertPRLookupOnly(t, out, d, gitRunner, tmuxRunner, ghRunner, repoRoot, prNumber)
}

// gh exiting 0 but printing nothing leaves us with no branch to check
// out, so PR must still refuse rather than build a worktree around an
// empty head ref.
func TestPR_EmptyHeadRef_ReturnsErrorAndCreatesNothing(t *testing.T) {
	forceGhAvailable(t)
	repoRoot := t.TempDir()
	prNumber := "42"

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"}, // rev-parse --show-toplevel
		},
	}
	tmuxRunner := &executil.FakeRunner{}
	ghRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: ""}, // gh pr view ... --jq .headRefName -> empty, exit 0
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	gh := ghutil.GH{Runner: ghRunner}

	var err error
	out := captureStdout(t, func() {
		err = PR(d, gh, prNumber, repoRoot, false, 1)
	})

	want := "gh reported no head branch for PR 42"
	if err == nil || err.Error() != want {
		t.Fatalf("PR error = %v, want %q", err, want)
	}
	assertPRLookupOnly(t, out, d, gitRunner, tmuxRunner, ghRunner, repoRoot, prNumber)
}

func TestPR_HappyPath_DetachedWorktreeThenGhCheckout(t *testing.T) {
	forceGhAvailable(t)
	setClaudeHome(t)
	repoRoot := t.TempDir()
	prNumber := "42"
	name := "pr-" + prNumber
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", name)
	repoName := filepath.Base(repoRoot)

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"}, // rev-parse --show-toplevel
			{},                        // worktree prune
			{},                        // worktree add --detach
		},
	}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("no server running")}, // list-sessions -> FindSessionID = ""
			{Stdout: "$1\n"},                       // new-session -> sessionID
			{},                                     // split-window -h
			{},                                     // split-window -v
			{},                                     // send-keys nvim
			{},                                     // send-keys claude
			{},                                     // attach-session
		},
	}
	ghRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "feature-branch\n"}, // gh pr view ... -> head ref
			{},                           // gh pr checkout
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	gh := ghutil.GH{Runner: ghRunner}

	if err := PR(d, gh, prNumber, repoRoot, false, 7); err != nil {
		t.Fatalf("PR returned error: %v", err)
	}

	wantGitCalls := []executil.Call{
		{Name: "git", Args: []string{"-C", repoRoot, "rev-parse", "--show-toplevel"}},
		{Name: "git", Args: []string{"-C", repoRoot, "worktree", "prune"}},
		{Name: "git", Args: []string{"-C", repoRoot, "worktree", "add", "--detach", worktreePath}},
	}
	if !reflect.DeepEqual(gitRunner.Calls, wantGitCalls) {
		t.Errorf("git Calls = %+v, want %+v (no fetch-origin call — pr has no base branch to fetch)", gitRunner.Calls, wantGitCalls)
	}

	wantGhCalls := []executil.Call{
		{Name: "sh", Args: []string{"-c", "cd " + "'" + repoRoot + "'" + " && 'gh' 'pr' 'view' '" + prNumber + "' '--json' 'headRefName' '--jq' '.headRefName'"}},
		{Name: "sh", Args: []string{"-c", "cd " + "'" + worktreePath + "'" + " && 'gh' 'pr' 'checkout' '" + prNumber + "'"}},
	}
	if !reflect.DeepEqual(ghRunner.Calls, wantGhCalls) {
		t.Errorf("gh Calls = %+v, want %+v (PRHeadRef lookup, then Checkout, in that order)", ghRunner.Calls, wantGhCalls)
	}

	windowTarget := "$1:" + repoName
	wantTmuxCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"new-session", "-d", "-s", name, "-n", repoName, "-c", worktreePath, "-P", "-F", "#{session_id}"}},
		{Name: "tmux", Args: []string{"split-window", "-h", "-p", "25", "-t", windowTarget, "-c", worktreePath}},
		{Name: "tmux", Args: []string{"split-window", "-v", "-t", windowTarget + ".1", "-c", worktreePath}},
		{Name: "tmux", Args: []string{"send-keys", "-t", windowTarget + ".0", "nvim .", "C-m"}},
		{Name: "tmux", Args: []string{"send-keys", "-t", windowTarget + ".2", "claude 'Please give a high level explanation of this PR and it'\\''s business purpose'", "C-m"}},
		{Name: "tmux", Args: []string{"attach-session", "-t", "$1"}, Interactive: true},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v", tmuxRunner.Calls, wantTmuxCalls)
	}

	if len(d.State.Sessions) != 1 {
		t.Fatalf("State.Sessions = %+v, want exactly 1", d.State.Sessions)
	}
	sess := d.State.Sessions[0]
	if sess.Name != name {
		t.Errorf("Sessions[0].Name = %q, want %q", sess.Name, name)
	}
	if sess.Description != "PR #"+prNumber {
		t.Errorf("Sessions[0].Description = %q, want %q", sess.Description, "PR #"+prNumber)
	}
}

func TestPR_LaunchesFixedPromptAndAlwaysAttaches(t *testing.T) {
	forceGhAvailable(t)
	setClaudeHome(t)
	repoRoot := t.TempDir()
	prNumber := "7"
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", "pr-"+prNumber)

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"},
			{},
			{},
		},
	}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("no server running")},
			{Stdout: "$9\n"},
			{}, {}, {}, {},
			{}, // switch-client (insideTmux = true)
		},
	}
	ghRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "some-branch\n"},
			{},
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	gh := ghutil.GH{Runner: ghRunner}

	if err := PR(d, gh, prNumber, repoRoot, true, 1); err != nil {
		t.Fatalf("PR returned error: %v", err)
	}

	lastCall := tmuxRunner.Calls[len(tmuxRunner.Calls)-1]
	wantLastCall := executil.Call{Name: "tmux", Args: []string{"switch-client", "-t", "$9"}}
	if !reflect.DeepEqual(lastCall, wantLastCall) {
		t.Errorf("last tmux Call = %+v, want %+v (PR must always attach/switch, using switch-client when insideTmux)", lastCall, wantLastCall)
	}

	repoName := filepath.Base(repoRoot)
	windowTarget := "$9:" + repoName
	wantPromptCall := executil.Call{
		Name: "tmux",
		Args: []string{"send-keys", "-t", windowTarget + ".2", "claude 'Please give a high level explanation of this PR and it'\\''s business purpose'", "C-m"},
	}
	found := false
	for _, call := range tmuxRunner.Calls {
		if reflect.DeepEqual(call, wantPromptCall) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("tmux Calls = %+v, want to include the fixed-prompt send-keys call %+v (not claudecmd.ResumeCommand)", tmuxRunner.Calls, wantPromptCall)
	}
	_ = worktreePath
}

func TestPR_NewWorktree_PrunesBeforeAddingDetached(t *testing.T) {
	forceGhAvailable(t)
	setClaudeHome(t)
	repoRoot := t.TempDir()
	prNumber := "99"
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", "pr-"+prNumber)

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"},
			{}, // worktree prune
			{}, // worktree add --detach
		},
	}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("no server running")},
			{Stdout: "$5\n"},
			{}, {}, {}, {},
			{},
		},
	}
	ghRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "fix-123\n"},
			{},
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	gh := ghutil.GH{Runner: ghRunner}

	if err := PR(d, gh, prNumber, repoRoot, false, 1); err != nil {
		t.Fatalf("PR returned error: %v", err)
	}

	wantGitCalls := []executil.Call{
		{Name: "git", Args: []string{"-C", repoRoot, "rev-parse", "--show-toplevel"}},
		{Name: "git", Args: []string{"-C", repoRoot, "worktree", "prune"}},
		{Name: "git", Args: []string{"-C", repoRoot, "worktree", "add", "--detach", worktreePath}},
	}
	if !reflect.DeepEqual(gitRunner.Calls, wantGitCalls) {
		t.Errorf("git Calls = %+v, want %+v (prune must happen before worktree add --detach, with no fetch-origin call)", gitRunner.Calls, wantGitCalls)
	}
}
