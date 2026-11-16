package mtcmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mauricebutts/motree/internal/executil"
	"github.com/mauricebutts/motree/internal/gitutil"
	"github.com/mauricebutts/motree/internal/state"
	"github.com/mauricebutts/motree/internal/tmux"
)

// newDeps builds a Deps wired to the given fake git/tmux runners and a
// fresh in-memory State.
func newDeps(gitRunner, tmuxRunner *executil.FakeRunner) *Deps {
	return &Deps{
		Git:   gitutil.Git{Runner: gitRunner},
		Tmux:  tmux.Tmux{Runner: tmuxRunner},
		State: &state.State{},
	}
}

// setClaudeHome points HOME at a fresh empty temp dir, so
// claudecmd.ResumeCommand deterministically returns "claude" (no prior
// transcript dir ever exists under it).
func setClaudeHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestCreate_NotInsideGitRepo_ReturnsExactErrorText(t *testing.T) {
	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("not a git repo")},
		},
	}
	tmuxRunner := &executil.FakeRunner{}
	d := newDeps(gitRunner, tmuxRunner)

	err := Create(d, "myname", "", "", "/tmp/not-a-repo", false, 1000)
	if err == nil {
		t.Fatalf("Create returned nil error, want %q", "motree: not inside a git repo")
	}
	if err.Error() != "motree: not inside a git repo" {
		t.Errorf("Create error = %q, want %q", err.Error(), "motree: not inside a git repo")
	}

	wantGitCalls := []executil.Call{
		{Name: "git", Args: []string{"-C", "/tmp/not-a-repo", "rev-parse", "--show-toplevel"}},
	}
	if !reflect.DeepEqual(gitRunner.Calls, wantGitCalls) {
		t.Errorf("git Calls = %+v, want %+v", gitRunner.Calls, wantGitCalls)
	}
	if len(tmuxRunner.Calls) != 0 {
		t.Errorf("tmux Calls = %+v, want none", tmuxRunner.Calls)
	}
	if len(d.State.Sessions) != 0 {
		t.Errorf("State.Sessions = %+v, want none", d.State.Sessions)
	}
}

func TestCreate_BrandNewSessionNewRepo_CreatesWorktreeBranchAndWindow(t *testing.T) {
	setClaudeHome(t)
	repoRoot := t.TempDir()
	name := "myname"
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", name)

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"},              // rev-parse --show-toplevel
			{Stdout: "refs/remotes/origin/main\n"}, // symbolic-ref (DefaultBranch)
			{Err: errors.New("no such ref")},       // show-ref (BranchExists -> false)
			{},                                     // worktree add -b
			{},                                     // branch --unset-upstream
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
	d := newDeps(gitRunner, tmuxRunner)

	if err := Create(d, name, "", "a description", repoRoot, false, 42); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	wantGitCalls := []executil.Call{
		{Name: "git", Args: []string{"-C", repoRoot, "rev-parse", "--show-toplevel"}},
		{Name: "git", Args: []string{"-C", repoRoot, "symbolic-ref", "refs/remotes/origin/HEAD"}},
		{Name: "git", Args: []string{"-C", repoRoot, "show-ref", "--verify", "--quiet", "refs/heads/" + name}},
		{Name: "git", Args: []string{"-C", repoRoot, "worktree", "add", "-b", name, worktreePath, "origin/main"}},
		{Name: "git", Args: []string{"-C", worktreePath, "branch", "--unset-upstream"}},
	}
	if !reflect.DeepEqual(gitRunner.Calls, wantGitCalls) {
		t.Errorf("git Calls = %+v, want %+v", gitRunner.Calls, wantGitCalls)
	}

	repoName := filepath.Base(repoRoot)
	windowTarget := "$1:" + repoName
	wantTmuxCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"new-session", "-d", "-s", name, "-n", repoName, "-c", worktreePath, "-P", "-F", "#{session_id}"}},
		{Name: "tmux", Args: []string{"split-window", "-h", "-p", "25", "-t", windowTarget, "-c", worktreePath}},
		{Name: "tmux", Args: []string{"split-window", "-v", "-t", windowTarget + ".1", "-c", worktreePath}},
		{Name: "tmux", Args: []string{"send-keys", "-t", windowTarget + ".0", "nvim .", "C-m"}},
		{Name: "tmux", Args: []string{"send-keys", "-t", windowTarget + ".2", "claude", "C-m"}},
		{Name: "tmux", Args: []string{"attach-session", "-t", "$1"}},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v", tmuxRunner.Calls, wantTmuxCalls)
	}
}

func TestCreate_ExistingSessionNewRepo_AddsWindowToExistingTmuxSession(t *testing.T) {
	setClaudeHome(t)
	repoRoot := t.TempDir()
	name := "myname"
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", name)
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("MkdirAll worktreePath: %v", err)
	}

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"}, // rev-parse --show-toplevel
		},
	}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$2:" + name + "\n"}, // list-sessions -> existing session found
			{Stdout: "other-repo\n"},      // list-windows -> this repo's window not present
			{},                            // new-window
			{},                            // split-window -h
			{},                            // split-window -v
			{},                            // send-keys nvim
			{},                            // send-keys claude
			{},                            // switch-client
		},
	}
	d := newDeps(gitRunner, tmuxRunner)

	// baseBranchFlag non-empty avoids needing a DefaultBranch git call, and
	// is irrelevant here since the worktree already exists.
	if err := Create(d, name, "irrelevant-base", "", repoRoot, true, 99); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	repoName := filepath.Base(repoRoot)
	windowTarget := "$2:" + repoName
	wantTmuxCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-windows", "-t", "$2", "-F", "#{window_name}"}},
		{Name: "tmux", Args: []string{"new-window", "-t", "$2", "-n", repoName, "-c", worktreePath}},
		{Name: "tmux", Args: []string{"split-window", "-h", "-p", "25", "-t", windowTarget, "-c", worktreePath}},
		{Name: "tmux", Args: []string{"split-window", "-v", "-t", windowTarget + ".1", "-c", worktreePath}},
		{Name: "tmux", Args: []string{"send-keys", "-t", windowTarget + ".0", "nvim .", "C-m"}},
		{Name: "tmux", Args: []string{"send-keys", "-t", windowTarget + ".2", "claude", "C-m"}},
		{Name: "tmux", Args: []string{"switch-client", "-t", "$2"}},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v", tmuxRunner.Calls, wantTmuxCalls)
	}

	// No worktree creation should have been attempted (only rev-parse).
	wantGitCalls := []executil.Call{
		{Name: "git", Args: []string{"-C", repoRoot, "rev-parse", "--show-toplevel"}},
	}
	if !reflect.DeepEqual(gitRunner.Calls, wantGitCalls) {
		t.Errorf("git Calls = %+v, want %+v", gitRunner.Calls, wantGitCalls)
	}
}

func TestCreate_ExistingSessionExistingRepo_IsNoop(t *testing.T) {
	repoRoot := t.TempDir()
	name := "myname"
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", name)
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("MkdirAll worktreePath: %v", err)
	}
	repoName := filepath.Base(repoRoot)

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"}, // rev-parse --show-toplevel
		},
	}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$3:" + name + "\n"}, // list-sessions -> existing session
			{Stdout: repoName + "\n"},     // list-windows -> window already present
			{},                            // attach-session
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{
		{
			Name:        name,
			Description: "original description",
			LastOpened:  111,
			Repos: []state.Repo{
				{RepoPath: repoRoot, WorktreePath: worktreePath},
			},
		},
	}

	if err := Create(d, name, "irrelevant-base", "", repoRoot, false, 222); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	wantTmuxCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-windows", "-t", "$3", "-F", "#{window_name}"}},
		{Name: "tmux", Args: []string{"attach-session", "-t", "$3"}},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v (no split/send-keys — window already existed)", tmuxRunner.Calls, wantTmuxCalls)
	}

	wantGitCalls := []executil.Call{
		{Name: "git", Args: []string{"-C", repoRoot, "rev-parse", "--show-toplevel"}},
	}
	if !reflect.DeepEqual(gitRunner.Calls, wantGitCalls) {
		t.Errorf("git Calls = %+v, want %+v (no worktree creation — worktree already existed)", gitRunner.Calls, wantGitCalls)
	}

	if len(d.State.Sessions) != 1 {
		t.Fatalf("State.Sessions = %+v, want exactly 1 (no duplicate)", d.State.Sessions)
	}
	sess := d.State.Sessions[0]
	if sess.LastOpened != 222 {
		t.Errorf("Sessions[0].LastOpened = %d, want 222 (timestamp refreshed)", sess.LastOpened)
	}
	if sess.Description != "original description" {
		t.Errorf("Sessions[0].Description = %q, want unchanged %q (empty description must not overwrite)", sess.Description, "original description")
	}
	if len(sess.Repos) != 1 {
		t.Errorf("Sessions[0].Repos = %+v, want exactly 1 (no duplicate repo row)", sess.Repos)
	}
}

func TestCreate_BaseBranchFlagOverridesOriginHEAD(t *testing.T) {
	setClaudeHome(t)
	repoRoot := t.TempDir()
	name := "myname"
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", name)

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"},        // rev-parse --show-toplevel
			{Err: errors.New("no such ref")}, // show-ref (BranchExists -> false)
			{},                               // worktree add -b ... origin/develop
			{},                               // branch --unset-upstream
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
	d := newDeps(gitRunner, tmuxRunner)

	if err := Create(d, name, "develop", "", repoRoot, false, 1); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	wantGitCalls := []executil.Call{
		{Name: "git", Args: []string{"-C", repoRoot, "rev-parse", "--show-toplevel"}},
		{Name: "git", Args: []string{"-C", repoRoot, "show-ref", "--verify", "--quiet", "refs/heads/" + name}},
		{Name: "git", Args: []string{"-C", repoRoot, "worktree", "add", "-b", name, worktreePath, "origin/develop"}},
		{Name: "git", Args: []string{"-C", worktreePath, "branch", "--unset-upstream"}},
	}
	if !reflect.DeepEqual(gitRunner.Calls, wantGitCalls) {
		t.Errorf("git Calls = %+v, want %+v (no symbolic-ref call — -b flag must override origin/HEAD lookup)", gitRunner.Calls, wantGitCalls)
	}
}

func TestCreate_SessionNameWithDot_ResolvesViaSessionIDNotRawName(t *testing.T) {
	setClaudeHome(t)
	repoRoot := t.TempDir()
	name := "feature.fix" // dotted name: must never be used directly as a tmux "-t" target
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", name)
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("MkdirAll worktreePath: %v", err)
	}
	repoName := filepath.Base(repoRoot)

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"},
		},
	}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$7:" + name + "\n"}, // list-sessions -> resolves dotted name to $7
			{Stdout: "other-repo\n"},      // list-windows -> no window for this repo yet
			{},                            // new-window
			{},                            // split-window -h
			{},                            // split-window -v
			{},                            // send-keys nvim
			{},                            // send-keys claude
			{},                            // attach-session
		},
	}
	d := newDeps(gitRunner, tmuxRunner)

	if err := Create(d, name, "irrelevant-base", "", repoRoot, false, 1); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	windowTarget := "$7:" + repoName
	wantTmuxCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-windows", "-t", "$7", "-F", "#{window_name}"}},
		{Name: "tmux", Args: []string{"new-window", "-t", "$7", "-n", repoName, "-c", worktreePath}},
		{Name: "tmux", Args: []string{"split-window", "-h", "-p", "25", "-t", windowTarget, "-c", worktreePath}},
		{Name: "tmux", Args: []string{"split-window", "-v", "-t", windowTarget + ".1", "-c", worktreePath}},
		{Name: "tmux", Args: []string{"send-keys", "-t", windowTarget + ".0", "nvim .", "C-m"}},
		{Name: "tmux", Args: []string{"send-keys", "-t", windowTarget + ".2", "claude", "C-m"}},
		{Name: "tmux", Args: []string{"attach-session", "-t", "$7"}},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v (every -t target must be the resolved session ID $7, never the dotted name %q)", tmuxRunner.Calls, wantTmuxCalls, name)
	}
}

func TestCreate_UpdatesStateUpsertAndEnsureRepo(t *testing.T) {
	setClaudeHome(t)
	repoRoot := t.TempDir()
	name := "myname"
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", name)

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"},
			{Stdout: "refs/remotes/origin/main\n"},
			{Err: errors.New("no such ref")},
			{},
			{},
		},
	}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("no server running")},
			{Stdout: "$9\n"},
			{}, {}, {}, {},
			{},
		},
	}
	d := newDeps(gitRunner, tmuxRunner)

	if err := Create(d, name, "", "my description", repoRoot, false, 12345); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if len(d.State.Sessions) != 1 {
		t.Fatalf("State.Sessions = %+v, want exactly 1", d.State.Sessions)
	}
	sess := d.State.Sessions[0]
	if sess.Name != name {
		t.Errorf("Sessions[0].Name = %q, want %q", sess.Name, name)
	}
	if sess.Description != "my description" {
		t.Errorf("Sessions[0].Description = %q, want %q", sess.Description, "my description")
	}
	if sess.LastOpened != 12345 {
		t.Errorf("Sessions[0].LastOpened = %d, want 12345", sess.LastOpened)
	}
	wantRepos := []state.Repo{{RepoPath: repoRoot, WorktreePath: worktreePath}}
	if !reflect.DeepEqual(sess.Repos, wantRepos) {
		t.Errorf("Sessions[0].Repos = %+v, want %+v", sess.Repos, wantRepos)
	}
}
