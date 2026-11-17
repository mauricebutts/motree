package mtcmd

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mauricebutts/motree/internal/executil"
	"github.com/mauricebutts/motree/internal/tmux"
)

func TestSpawn_ExistingSessionName_ReturnsError(t *testing.T) {
	name := "myname"
	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$1:" + name + "\n"}, // list-sessions -> session already exists
		},
	}
	d := newDeps(gitRunner, tmuxRunner)

	err := Spawn(d, name, "do the thing", "", "", "/tmp/whatever", 1)
	if err == nil {
		t.Fatalf("Spawn returned nil error, want an error — session already exists")
	}

	wantTmuxCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v (must stop at the existence check)", tmuxRunner.Calls, wantTmuxCalls)
	}
	if len(gitRunner.Calls) != 0 {
		t.Errorf("git Calls = %+v, want none — must never touch git when the session already exists", gitRunner.Calls)
	}
	if len(d.State.Sessions) != 0 {
		t.Errorf("State.Sessions = %+v, want none", d.State.Sessions)
	}
}

func TestSpawn_CreatesWorktreeAndSizedSession(t *testing.T) {
	setClaudeHome(t)
	repoRoot := t.TempDir()
	name := "myname"
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", name)
	repoName := filepath.Base(repoRoot)

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"},              // rev-parse --show-toplevel
			{Stdout: "refs/remotes/origin/main\n"}, // symbolic-ref (DefaultBranch)
			{},                                     // worktree prune
			{},                                     // fetch origin main
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
		},
	}
	d := newDeps(gitRunner, tmuxRunner)

	if err := Spawn(d, name, "do the thing", "", "a description", repoRoot, 42); err != nil {
		t.Fatalf("Spawn returned error: %v", err)
	}

	wantGitCalls := []executil.Call{
		{Name: "git", Args: []string{"-C", repoRoot, "rev-parse", "--show-toplevel"}},
		{Name: "git", Args: []string{"-C", repoRoot, "symbolic-ref", "refs/remotes/origin/HEAD"}},
		{Name: "git", Args: []string{"-C", repoRoot, "worktree", "prune"}},
		{Name: "git", Args: []string{"-C", repoRoot, "fetch", "origin", "main"}},
		{Name: "git", Args: []string{"-C", repoRoot, "show-ref", "--verify", "--quiet", "refs/heads/" + name}},
		{Name: "git", Args: []string{"-C", repoRoot, "worktree", "add", "-b", name, worktreePath, "origin/main"}},
		{Name: "git", Args: []string{"-C", worktreePath, "branch", "--unset-upstream"}},
	}
	if !reflect.DeepEqual(gitRunner.Calls, wantGitCalls) {
		t.Errorf("git Calls = %+v, want %+v", gitRunner.Calls, wantGitCalls)
	}

	windowTarget := "$1:" + repoName
	wantTmuxCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"new-session", "-d", "-s", name, "-n", repoName, "-c", worktreePath, "-x", "200", "-y", "50", "-P", "-F", "#{session_id}"}},
		{Name: "tmux", Args: []string{"split-window", "-h", "-p", "25", "-t", windowTarget, "-c", worktreePath}},
		{Name: "tmux", Args: []string{"split-window", "-v", "-t", windowTarget + ".1", "-c", worktreePath}},
		{Name: "tmux", Args: []string{"send-keys", "-t", windowTarget + ".0", "nvim .", "C-m"}},
		{Name: "tmux", Args: []string{"send-keys", "-t", windowTarget + ".2", "claude 'do the thing'", "C-m"}},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v", tmuxRunner.Calls, wantTmuxCalls)
	}

	if len(d.State.Sessions) != 1 {
		t.Fatalf("State.Sessions = %+v, want exactly 1", d.State.Sessions)
	}
	sess := d.State.Sessions[0]
	if sess.Description != "a description" {
		t.Errorf("Sessions[0].Description = %q, want %q", sess.Description, "a description")
	}
}

func TestSpawn_SendsQuotedPromptToClaude_NeverAttaches(t *testing.T) {
	setClaudeHome(t)
	repoRoot := t.TempDir()
	name := "myname"
	repoName := filepath.Base(repoRoot)
	prompt := `fix the "bug" in $HOME's config; use ` + "`backticks`" + ` and don't break it`

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"},
			{Stdout: "refs/remotes/origin/main\n"},
			{},
			{},
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
		},
	}
	d := newDeps(gitRunner, tmuxRunner)

	if err := Spawn(d, name, prompt, "", "", repoRoot, 1); err != nil {
		t.Fatalf("Spawn returned error: %v", err)
	}

	windowTarget := "$9:" + repoName
	wantLastCall := executil.Call{
		Name: "tmux",
		Args: []string{"send-keys", "-t", windowTarget + ".2", "claude " + tmux.Quote(prompt), "C-m"},
	}
	gotLastCall := tmuxRunner.Calls[len(tmuxRunner.Calls)-1]
	if !reflect.DeepEqual(gotLastCall, wantLastCall) {
		t.Errorf("last tmux Call = %+v, want %+v (prompt must be quoted via tmux.Quote, sent as the final call)", gotLastCall, wantLastCall)
	}

	for _, call := range tmuxRunner.Calls {
		if call.Name == "tmux" && len(call.Args) > 0 && (call.Args[0] == "attach-session" || call.Args[0] == "switch-client") {
			t.Errorf("tmux Calls = %+v, want no attach-session/switch-client call — Spawn must never attach", tmuxRunner.Calls)
		}
	}
}

func TestSpawn_NewWorktree_PrunesAndFetchesOriginBeforeCreating(t *testing.T) {
	setClaudeHome(t)
	repoRoot := t.TempDir()
	name := "myname"
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", name)

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"},
			{},                               // worktree prune
			{},                               // fetch origin develop
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
		},
	}
	d := newDeps(gitRunner, tmuxRunner)

	if err := Spawn(d, name, "do the thing", "develop", "", repoRoot, 1); err != nil {
		t.Fatalf("Spawn returned error: %v", err)
	}

	wantGitCalls := []executil.Call{
		{Name: "git", Args: []string{"-C", repoRoot, "rev-parse", "--show-toplevel"}},
		{Name: "git", Args: []string{"-C", repoRoot, "worktree", "prune"}},
		{Name: "git", Args: []string{"-C", repoRoot, "fetch", "origin", "develop"}},
		{Name: "git", Args: []string{"-C", repoRoot, "show-ref", "--verify", "--quiet", "refs/heads/" + name}},
		{Name: "git", Args: []string{"-C", repoRoot, "worktree", "add", "-b", name, worktreePath, "origin/develop"}},
		{Name: "git", Args: []string{"-C", worktreePath, "branch", "--unset-upstream"}},
	}
	if !reflect.DeepEqual(gitRunner.Calls, wantGitCalls) {
		t.Errorf("git Calls = %+v, want %+v (prune, then fetch, must both happen before the branch-exists check / worktree-add call, and only on the new-worktree path, with no symbolic-ref call since -b was given)", gitRunner.Calls, wantGitCalls)
	}
}

func TestSpawn_FetchFailure_IsNonFatal_WarnsAndContinuesWithLocalRefs(t *testing.T) {
	setClaudeHome(t)
	repoRoot := t.TempDir()
	name := "myname"
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", name)

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: repoRoot + "\n"},
			{Stdout: "refs/remotes/origin/main\n"},
			{},                                       // worktree prune
			{Err: errors.New("network unreachable")}, // fetch origin main -> fails
			{Err: errors.New("no such ref")},
			{},
			{},
		},
	}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("no server running")},
			{Stdout: "$13\n"},
			{}, {}, {}, {},
		},
	}
	d := newDeps(gitRunner, tmuxRunner)

	err := Spawn(d, name, "do the thing", "", "", repoRoot, 1)
	if err != nil {
		t.Fatalf("Spawn returned error: %v, want nil — a fetch failure must be non-fatal", err)
	}

	wantGitCalls := []executil.Call{
		{Name: "git", Args: []string{"-C", repoRoot, "rev-parse", "--show-toplevel"}},
		{Name: "git", Args: []string{"-C", repoRoot, "symbolic-ref", "refs/remotes/origin/HEAD"}},
		{Name: "git", Args: []string{"-C", repoRoot, "worktree", "prune"}},
		{Name: "git", Args: []string{"-C", repoRoot, "fetch", "origin", "main"}},
		{Name: "git", Args: []string{"-C", repoRoot, "show-ref", "--verify", "--quiet", "refs/heads/" + name}},
		{Name: "git", Args: []string{"-C", repoRoot, "worktree", "add", "-b", name, worktreePath, "origin/main"}},
		{Name: "git", Args: []string{"-C", worktreePath, "branch", "--unset-upstream"}},
	}
	if !reflect.DeepEqual(gitRunner.Calls, wantGitCalls) {
		t.Errorf("git Calls = %+v, want %+v (branch/worktree creation must still proceed despite the fetch failure)", gitRunner.Calls, wantGitCalls)
	}
}

func TestSpawn_NotInsideGitRepo_ReturnsExactErrorText(t *testing.T) {
	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("not a git repo")},
		},
	}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("no server running")}, // list-sessions -> FindSessionID = ""
		},
	}
	d := newDeps(gitRunner, tmuxRunner)

	err := Spawn(d, "myname", "do the thing", "", "", "/tmp/not-a-repo", 1000)
	if err == nil {
		t.Fatalf("Spawn returned nil error, want %q", "motree: not inside a git repo")
	}
	if err.Error() != "motree: not inside a git repo" {
		t.Errorf("Spawn error = %q, want %q", err.Error(), "motree: not inside a git repo")
	}
}
