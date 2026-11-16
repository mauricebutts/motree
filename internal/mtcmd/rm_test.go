package mtcmd

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mauricebutts/motree/internal/executil"
	"github.com/mauricebutts/motree/internal/state"
)

func TestRemoveSession_NoneTracked_ReturnsError(t *testing.T) {
	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{}
	d := newDeps(gitRunner, tmuxRunner)

	err := RemoveSession(d, "ghost")
	if err == nil {
		t.Fatalf("RemoveSession returned nil error, want an error — nothing is tracked for 'ghost'")
	}
	wantMsg := "mt: nothing recorded for session 'ghost'"
	if err.Error() != wantMsg {
		t.Errorf("RemoveSession error = %q, want %q", err.Error(), wantMsg)
	}

	if len(gitRunner.Calls) != 0 || len(tmuxRunner.Calls) != 0 {
		t.Errorf("Calls: git=%+v tmux=%+v, want none", gitRunner.Calls, tmuxRunner.Calls)
	}
}

func TestRemoveSession_WorktreeRemoveFails_AbortsBeforeStateChange(t *testing.T) {
	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stderr: "fatal: 'myfeature' is dirty, use --force to delete it", Err: errors.New("exit status 1")}, // repo1 fails
			{}, // repo2 still attempted and succeeds
		},
	}
	tmuxRunner := &executil.FakeRunner{}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{
		{
			Name: "myfeature",
			Repos: []state.Repo{
				{RepoPath: "/repo/one", WorktreePath: "/repo/one/.claude/worktrees/myfeature"},
				{RepoPath: "/repo/two", WorktreePath: "/repo/two/.claude/worktrees/myfeature"},
			},
		},
	}

	var err error
	stderr := captureStderr(t, func() {
		err = RemoveSession(d, "myfeature")
	})

	if err == nil {
		t.Fatalf("RemoveSession returned nil error, want an error — a worktree remove failed")
	}
	if !strings.Contains(stderr, "fatal: 'myfeature' is dirty, use --force to delete it") {
		t.Errorf("stderr = %q, want it to contain the real git error text", stderr)
	}

	wantGitCalls := []executil.Call{
		{Name: "git", Args: []string{"-C", "/repo/one", "worktree", "remove", "/repo/one/.claude/worktrees/myfeature"}},
		{Name: "git", Args: []string{"-C", "/repo/two", "worktree", "remove", "/repo/two/.claude/worktrees/myfeature"}},
	}
	if !reflect.DeepEqual(gitRunner.Calls, wantGitCalls) {
		t.Errorf("git Calls = %+v, want %+v (every repo's worktree removal must be attempted, but no branch delete)", gitRunner.Calls, wantGitCalls)
	}
	if len(tmuxRunner.Calls) != 0 {
		t.Errorf("tmux Calls = %+v, want none — must abort before ever touching tmux", tmuxRunner.Calls)
	}
	if len(d.State.Sessions) != 1 {
		t.Errorf("State.Sessions = %+v, want still 1 — must abort before any state mutation", d.State.Sessions)
	}
}

func TestRemoveSession_HappyPath_RemovesWorktreesBranchSessionAndState(t *testing.T) {
	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{}, // worktree remove repo1
			{}, // worktree remove repo2
			{}, // branch delete repo1
			{}, // branch delete repo2
		},
	}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$3:myfeature\n"}, // list-sessions -> FindSessionID
			{},                         // kill-session
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{
		{
			Name: "myfeature",
			Repos: []state.Repo{
				{RepoPath: "/repo/one", WorktreePath: "/repo/one/.claude/worktrees/myfeature"},
				{RepoPath: "/repo/two", WorktreePath: "/repo/two/.claude/worktrees/myfeature"},
			},
		},
	}

	var err error
	stdout := captureStdout(t, func() {
		err = RemoveSession(d, "myfeature")
	})
	if err != nil {
		t.Fatalf("RemoveSession returned error: %v", err)
	}
	wantStdout := "mt: removed session 'myfeature'\n"
	if stdout != wantStdout {
		t.Errorf("stdout = %q, want %q", stdout, wantStdout)
	}

	wantGitCalls := []executil.Call{
		{Name: "git", Args: []string{"-C", "/repo/one", "worktree", "remove", "/repo/one/.claude/worktrees/myfeature"}},
		{Name: "git", Args: []string{"-C", "/repo/two", "worktree", "remove", "/repo/two/.claude/worktrees/myfeature"}},
		{Name: "git", Args: []string{"-C", "/repo/one", "branch", "-d", "myfeature"}},
		{Name: "git", Args: []string{"-C", "/repo/two", "branch", "-d", "myfeature"}},
	}
	if !reflect.DeepEqual(gitRunner.Calls, wantGitCalls) {
		t.Errorf("git Calls = %+v, want %+v", gitRunner.Calls, wantGitCalls)
	}

	wantTmuxCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"kill-session", "-t", "$3"}},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v", tmuxRunner.Calls, wantTmuxCalls)
	}

	if len(d.State.Sessions) != 0 {
		t.Errorf("State.Sessions = %+v, want none — session must be removed from state", d.State.Sessions)
	}
}

func TestRemoveSession_TmuxSessionAlreadyGone_StillRemovesRest(t *testing.T) {
	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{}, // worktree remove
			{}, // branch delete
		},
	}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("no server running")}, // list-sessions -> FindSessionID = ""
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{
		{
			Name: "myfeature",
			Repos: []state.Repo{
				{RepoPath: "/repo/one", WorktreePath: "/repo/one/.claude/worktrees/myfeature"},
			},
		},
	}

	var err error
	stdout := captureStdout(t, func() {
		err = RemoveSession(d, "myfeature")
	})
	if err != nil {
		t.Fatalf("RemoveSession returned error: %v", err)
	}
	wantStdout := "mt: removed session 'myfeature'\n"
	if stdout != wantStdout {
		t.Errorf("stdout = %q, want %q", stdout, wantStdout)
	}

	wantTmuxCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v (no session found -> no kill-session call)", tmuxRunner.Calls, wantTmuxCalls)
	}
	if len(d.State.Sessions) != 0 {
		t.Errorf("State.Sessions = %+v, want none", d.State.Sessions)
	}
}

func TestRemoveSession_BranchDeleteFailure_IsIgnored(t *testing.T) {
	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{}, // worktree remove succeeds
			{Err: errors.New("error: branch 'myfeature' not fully merged")}, // branch delete fails
		},
	}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("no server running")},
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{
		{
			Name: "myfeature",
			Repos: []state.Repo{
				{RepoPath: "/repo/one", WorktreePath: "/repo/one/.claude/worktrees/myfeature"},
			},
		},
	}

	var err error
	captureStdout(t, func() {
		err = RemoveSession(d, "myfeature")
	})
	if err != nil {
		t.Fatalf("RemoveSession returned error: %v, want nil — a branch delete failure must be ignored", err)
	}
	if len(d.State.Sessions) != 0 {
		t.Errorf("State.Sessions = %+v, want none — branch delete failure must not block the rest of removal", d.State.Sessions)
	}
}

func TestRm_NoSessionsTracked_PrintsMessageNoFzfCall(t *testing.T) {
	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{}
	d := newDeps(gitRunner, tmuxRunner)

	var err error
	stderr := captureStderr(t, func() {
		err = Rm(d, "/tmp")
	})

	if err != nil {
		t.Fatalf("Rm returned error: %v", err)
	}
	if stderr != "mt: no sessions tracked yet\n" {
		t.Errorf("stderr = %q, want %q", stderr, "mt: no sessions tracked yet\n")
	}
	if len(tmuxRunner.Calls) != 0 {
		t.Errorf("tmux Calls = %+v, want none — must return before ever touching fzf/tmux", tmuxRunner.Calls)
	}
}

func TestRm_NoFzfInstalled_ReturnsError(t *testing.T) {
	forceFzfUnavailable(t)

	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{{Name: "myfeature"}}

	// Rm must not print this message to stderr itself: its caller
	// (cmd/mt/main.go) prints every non-nil returned error via
	// "mt: %v\n" already, so an explicit Fprintln here would put the same
	// line on stderr twice. Returning the message as the error's text is
	// the only print.
	var err error
	stderr := captureStderr(t, func() {
		err = Rm(d, "/tmp")
	})

	if err == nil {
		t.Fatalf("Rm returned nil error, want an error — fzf isn't installed, and mt rm (unlike mt ls) has no plain-listing fallback")
	}
	wantErrMsg := "mt rm needs fzf installed to pick a session."
	if err.Error() != wantErrMsg {
		t.Errorf("Rm error = %q, want %q", err.Error(), wantErrMsg)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty — Rm must not print the message itself (its caller prints the returned error)", stderr)
	}
	if len(tmuxRunner.Calls) != 0 {
		t.Errorf("tmux Calls = %+v, want none", tmuxRunner.Calls)
	}
}

func TestRm_NoSelection_ReturnsNilNoRemoval(t *testing.T) {
	forceFzfAvailable(t)

	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: ""}, // fzf picker: cancelled, no selection
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{{Name: "myfeature"}}

	if err := Rm(d, "/tmp"); err != nil {
		t.Fatalf("Rm returned error: %v", err)
	}

	if len(gitRunner.Calls) != 0 {
		t.Errorf("git Calls = %+v, want none — no selection means nothing is removed", gitRunner.Calls)
	}
	if len(tmuxRunner.Calls) != 1 {
		t.Errorf("tmux Calls = %+v, want exactly 1 (the fzf run itself)", tmuxRunner.Calls)
	}
	if len(d.State.Sessions) != 1 {
		t.Errorf("State.Sessions = %+v, want unchanged", d.State.Sessions)
	}
}

func TestRm_HappyPath_PicksAndRemovesSelectedSession(t *testing.T) {
	forceFzfAvailable(t)

	gitRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{}, // worktree remove
			{}, // branch delete
		},
	}

	var fzfScript string
	tmuxRunner := &executil.FakeRunner{
		Responses: func(call executil.Call) (string, string, error) {
			switch call.Name {
			case "sh":
				fzfScript = call.Args[1]
				return "myfeature\tsome-label\tsome-desc\n", "", nil
			case "tmux":
				if len(call.Args) > 0 && call.Args[0] == "list-sessions" {
					return "", "", errors.New("no server running")
				}
			}
			return "", "", nil
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{
		{
			Name: "myfeature",
			Repos: []state.Repo{
				{RepoPath: "/repo/one", WorktreePath: "/repo/one/.claude/worktrees/myfeature"},
			},
		},
	}

	var err error
	stdout := captureStdout(t, func() {
		err = Rm(d, "/tmp")
	})
	if err != nil {
		t.Fatalf("Rm returned error: %v", err)
	}
	wantStdout := "mt: removed session 'myfeature'\n"
	if stdout != wantStdout {
		t.Errorf("stdout = %q, want %q", stdout, wantStdout)
	}

	wantGitCalls := []executil.Call{
		{Name: "git", Args: []string{"-C", "/repo/one", "worktree", "remove", "/repo/one/.claude/worktrees/myfeature"}},
		{Name: "git", Args: []string{"-C", "/repo/one", "branch", "-d", "myfeature"}},
	}
	if !reflect.DeepEqual(gitRunner.Calls, wantGitCalls) {
		t.Errorf("git Calls = %+v, want %+v", gitRunner.Calls, wantGitCalls)
	}

	wantFzfPrefix := `'fzf' '--delimiter=\t' '--with-nth=2' '--height=~40%' '--reverse' '--prompt=remove> ' '--preview=echo {3}' '--preview-window=down,3,wrap' < '`
	if !strings.HasPrefix(fzfScript, wantFzfPrefix) {
		t.Errorf("fzf script = %q, want prefix %q (mt rm's fzf invocation, ported from bash do_rm)", fzfScript, wantFzfPrefix)
	}

	wantTmuxCalls := []executil.Call{
		{Name: "sh", Args: []string{"-c", fzfScript}},
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v (fzf run, then FindSessionID — no kill-session since none is running)", tmuxRunner.Calls, wantTmuxCalls)
	}

	if len(d.State.Sessions) != 0 {
		t.Errorf("State.Sessions = %+v, want none", d.State.Sessions)
	}
}
