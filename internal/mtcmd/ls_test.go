package mtcmd

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/mauricebutts/motree/internal/executil"
	"github.com/mauricebutts/motree/internal/fzfutil"
	"github.com/mauricebutts/motree/internal/state"
	"github.com/mauricebutts/motree/internal/statusfile"
)

// setStatusHome points MOTREE_STATE_FILE at a fresh temp file, so
// statusfile.Read/Write operate against an isolated, empty status
// directory for the duration of the test.
func setStatusHome(t *testing.T) {
	t.Helper()
	t.Setenv("MOTREE_STATE_FILE", filepath.Join(t.TempDir(), ".motree"))
}

// forceFzfUnavailable overrides fzfLookPath so Ls's "fzf isn't installed"
// fallback branch fires deterministically, independent of whether fzf
// actually happens to be installed on whatever machine runs `go test`.
func forceFzfUnavailable(t *testing.T) {
	t.Helper()
	orig := fzfLookPath
	fzfLookPath = func(file string) (string, error) { return "", errors.New("executable file not found in $PATH") }
	t.Cleanup(func() { fzfLookPath = orig })
}

// forceFzfAvailable overrides fzfLookPath so Ls proceeds past its "fzf
// isn't installed" check deterministically, independent of whether fzf
// actually happens to be installed on whatever machine runs `go test`.
func forceFzfAvailable(t *testing.T) {
	t.Helper()
	orig := fzfLookPath
	fzfLookPath = func(file string) (string, error) { return "/usr/local/bin/" + file, nil }
	t.Cleanup(func() { fzfLookPath = orig })
}

// forceCurlAvailableForLs overrides fzfutil.CurlLookPath (exported for
// exactly this cross-package purpose) so SupportsListen's curl check
// deterministically succeeds, independent of whether curl actually
// happens to be installed on whatever machine runs `go test`.
func forceCurlAvailableForLs(t *testing.T) {
	t.Helper()
	orig := fzfutil.CurlLookPath
	fzfutil.CurlLookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	t.Cleanup(func() { fzfutil.CurlLookPath = orig })
}

// captureStderr runs fn with os.Stderr redirected, returning whatever it
// wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return string(out)
}

func TestSessionStatus_PriorityWaitingOverRunningOverSubagentsOverStopped(t *testing.T) {
	setStatusHome(t)

	repos := []state.Repo{
		{RepoPath: "/repo/stopped", WorktreePath: t.TempDir()},
		{RepoPath: "/repo/subagents", WorktreePath: t.TempDir()},
		{RepoPath: "/repo/running", WorktreePath: t.TempDir()},
		{RepoPath: "/repo/waiting", WorktreePath: t.TempDir()},
	}
	if err := statusfile.Write(repos[0].WorktreePath, statusfile.Stopped); err != nil {
		t.Fatalf("Write stopped: %v", err)
	}
	if err := statusfile.Write(repos[1].WorktreePath, statusfile.Subagents); err != nil {
		t.Fatalf("Write subagents: %v", err)
	}
	if err := statusfile.Write(repos[2].WorktreePath, statusfile.Running); err != nil {
		t.Fatalf("Write running: %v", err)
	}
	if err := statusfile.Write(repos[3].WorktreePath, statusfile.Waiting); err != nil {
		t.Fatalf("Write waiting: %v", err)
	}

	status, found := SessionStatus(repos)
	if !found {
		t.Fatalf("SessionStatus found = false, want true")
	}
	if status != statusfile.Waiting {
		t.Errorf("SessionStatus = %q, want %q (waiting must win over running/subagents/stopped)", status, statusfile.Waiting)
	}

	// Drop waiting: running should now win over subagents/stopped.
	status, found = SessionStatus(repos[:3])
	if !found || status != statusfile.Running {
		t.Errorf("SessionStatus(no waiting) = (%q, %v), want (%q, true)", status, found, statusfile.Running)
	}

	// Drop running too: subagents should win over stopped.
	status, found = SessionStatus(repos[:2])
	if !found || status != statusfile.Subagents {
		t.Errorf("SessionStatus(no waiting/running) = (%q, %v), want (%q, true)", status, found, statusfile.Subagents)
	}

	// Only stopped left.
	status, found = SessionStatus(repos[:1])
	if !found || status != statusfile.Stopped {
		t.Errorf("SessionStatus(only stopped) = (%q, %v), want (%q, true)", status, found, statusfile.Stopped)
	}
}

func TestSessionStatus_NoStatusFileAnywhere_NotFound(t *testing.T) {
	setStatusHome(t)

	repos := []state.Repo{
		{RepoPath: "/repo/a", WorktreePath: t.TempDir()},
		{RepoPath: "/repo/b", WorktreePath: t.TempDir()},
	}

	status, found := SessionStatus(repos)
	if found {
		t.Errorf("SessionStatus found = true, want false (no repo has a status file)")
	}
	if status != "" {
		t.Errorf("SessionStatus status = %q, want empty", status)
	}

	// Also true for a session with no repos at all.
	status, found = SessionStatus(nil)
	if found || status != "" {
		t.Errorf("SessionStatus(nil) = (%q, %v), want (\"\", false)", status, found)
	}
}

func TestFormatLabel_MatchesEmojiDateReposDescriptionFormat(t *testing.T) {
	sess := state.Session{
		Name:        "myfeature",
		Description: "fix the thing",
		LastOpened:  1234567890,
		Repos: []state.Repo{
			{RepoPath: "/Users/x/Projects/foo", WorktreePath: "/whatever/foo"},
			{RepoPath: "/Users/x/Projects/bar", WorktreePath: "/whatever/bar"},
		},
	}

	got := FormatLabel(sess, statusfile.Waiting, true)

	wantDate := timeFormatForTest(1234567890)
	want := "🔔 myfeature | " + wantDate + " (foo,bar) — fix the thing"
	if got != want {
		t.Errorf("FormatLabel = %q, want %q", got, want)
	}
}

func TestFormatLabel_NoDescription_ShowsPlaceholder(t *testing.T) {
	sess := state.Session{
		Name: "myfeature",
		Repos: []state.Repo{
			{RepoPath: "/Users/x/Projects/foo", WorktreePath: "/whatever/foo"},
		},
	}

	got := FormatLabel(sess, "", false)

	want := "myfeature (foo) — (no description)"
	if got != want {
		t.Errorf("FormatLabel = %q, want %q", got, want)
	}
}

func TestFormatLabel_NoStatusFound_NoEmojiPrefix(t *testing.T) {
	sess := state.Session{Name: "myfeature"}

	got := FormatLabel(sess, statusfile.Running, false)

	want := "myfeature — (no description)"
	if got != want {
		t.Errorf("FormatLabel = %q, want %q (found=false must suppress the emoji even though a status value was passed)", got, want)
	}
}

func TestFormatLabel_ZeroLastOpened_OmitsDateSegment(t *testing.T) {
	sess := state.Session{
		Name:       "myfeature",
		LastOpened: 0,
	}

	got := FormatLabel(sess, "", false)

	want := "myfeature — (no description)"
	if got != want {
		t.Errorf("FormatLabel = %q, want %q (LastOpened==0 must omit the date segment)", got, want)
	}
}

func TestFormatLabel_StoppedEmoji(t *testing.T) {
	sess := state.Session{Name: "myfeature"}

	got := FormatLabel(sess, statusfile.Stopped, true)

	want := "🛑 myfeature — (no description)"
	if got != want {
		t.Errorf("FormatLabel = %q, want %q", got, want)
	}
}

func TestFormatLabel_SubagentsEmoji(t *testing.T) {
	sess := state.Session{Name: "myfeature"}

	got := FormatLabel(sess, statusfile.Subagents, true)

	want := "🤖 myfeature — (no description)"
	if got != want {
		t.Errorf("FormatLabel = %q, want %q", got, want)
	}
}

func TestRenderRows_SortedMostRecentFirst(t *testing.T) {
	setStatusHome(t)

	st := &state.State{
		Sessions: []state.Session{
			{Name: "older", Description: "d1", LastOpened: 100, Repos: []state.Repo{{RepoPath: "/r/older", WorktreePath: t.TempDir()}}},
			{Name: "newer", Description: "d2", LastOpened: 200, Repos: []state.Repo{{RepoPath: "/r/newer", WorktreePath: t.TempDir()}}},
			{Name: "never-opened", Description: "", LastOpened: 0, Repos: []state.Repo{{RepoPath: "/r/never", WorktreePath: t.TempDir()}}},
		},
	}

	rows := RenderRows(st)
	if len(rows) != 3 {
		t.Fatalf("RenderRows returned %d rows, want 3", len(rows))
	}

	wantOrder := []string{"newer", "older", "never-opened"}
	for i, wantName := range wantOrder {
		gotName := rows[i][:len(wantName)]
		if gotName != wantName || rows[i][len(wantName)] != '\t' {
			t.Errorf("rows[%d] = %q, want it to start with %q (sorted most- to least-recently-opened, 0 last)", i, rows[i], wantName+"\t")
		}
	}

	wantLast := "never-opened\tnever-opened (never) — (no description)\t(no description)"
	if rows[2] != wantLast {
		t.Errorf("rows[2] = %q, want %q", rows[2], wantLast)
	}
}

func TestPreview_NoClaudePaneFound_ReturnsDescriptionOnly(t *testing.T) {
	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$1:myfeature\n"}, // list-sessions -> session exists
			{Stdout: "other\n"},        // list-panes -> no claude pane in this window
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{
		{
			Name:        "myfeature",
			Description: "fix the thing",
			Repos:       []state.Repo{{RepoPath: "/Users/x/Projects/foo", WorktreePath: "/whatever/foo"}},
		},
	}

	got := Preview(d, "myfeature")

	want := "fix the thing"
	if got != want {
		t.Errorf("Preview = %q, want %q", got, want)
	}
}

func TestPreview_NoDescription_ReturnsPlaceholder(t *testing.T) {
	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: nil, Stdout: ""}, // list-sessions -> no server / not found
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{
		{Name: "myfeature", Description: ""},
	}

	got := Preview(d, "myfeature")

	want := "(no description)"
	if got != want {
		t.Errorf("Preview = %q, want %q", got, want)
	}
}

func TestPreview_UnknownSession_ReturnsPlaceholder(t *testing.T) {
	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{}
	d := newDeps(gitRunner, tmuxRunner)

	got := Preview(d, "nonexistent")

	want := "(no description)"
	if got != want {
		t.Errorf("Preview = %q, want %q", got, want)
	}
	if len(tmuxRunner.Calls) != 0 {
		t.Errorf("tmux Calls = %+v, want none — unknown session must never touch tmux", tmuxRunner.Calls)
	}
}

func TestPreview_ClaudePaneFound_ReturnsDescriptionDividerAndPaneTail(t *testing.T) {
	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$1:myfeature\n"}, // list-sessions -> session exists
			{Stdout: "0 claude\n"},     // list-panes -> pane 0 runs claude
			{Stdout: "line1\nline2\n"}, // capture-pane
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{
		{
			Name:        "myfeature",
			Description: "fix the thing",
			Repos:       []state.Repo{{RepoPath: "/Users/x/Projects/foo", WorktreePath: "/whatever/foo"}},
		},
	}

	got := Preview(d, "myfeature")

	want := "fix the thing\n" + previewDivider + "\nline1\nline2"
	if got != want {
		t.Errorf("Preview = %q, want %q", got, want)
	}

	wantTmuxCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-panes", "-t", "$1:foo", "-F", "#{pane_index} #{pane_current_command}"}},
		{Name: "tmux", Args: []string{"capture-pane", "-t", "$1:foo.0", "-p"}},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v", tmuxRunner.Calls, wantTmuxCalls)
	}
}

// timeFormatForTest mirrors FormatLabel's exact date formatting so the test
// doesn't hardcode a timezone-dependent string.
func timeFormatForTest(ts int64) string {
	return formatTimeForLabel(ts)
}

func TestLs_NoSessionsTracked_PrintsMessageNoFzfCall(t *testing.T) {
	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{}
	d := newDeps(gitRunner, tmuxRunner)

	var err error
	stderr := captureStderr(t, func() {
		err = Ls(d, "/usr/local/bin/mt", "/tmp", false)
	})

	if err != nil {
		t.Fatalf("Ls returned error: %v", err)
	}
	if stderr != "mt: no sessions tracked yet\n" {
		t.Errorf("stderr = %q, want %q", stderr, "mt: no sessions tracked yet\n")
	}
	if len(tmuxRunner.Calls) != 0 {
		t.Errorf("tmux Calls = %+v, want none — must return before ever touching fzf/tmux", tmuxRunner.Calls)
	}
}

func TestLs_NoFzfInstalled_FallsBackToPlainListing(t *testing.T) {
	setStatusHome(t)
	forceFzfUnavailable(t)

	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{
		{
			Name:        "myfeature",
			Description: "fix the thing",
			Repos:       []state.Repo{{RepoPath: "/Users/x/Projects/foo", WorktreePath: t.TempDir()}},
		},
	}

	var err error
	stdout := captureStdout(t, func() {
		err = Ls(d, "/usr/local/bin/mt", "/tmp", false)
	})

	if err != nil {
		t.Fatalf("Ls returned error: %v", err)
	}

	wantLabel := FormatLabel(d.State.Sessions[0], "", false)
	wantOut := wantLabel + "\n  /Users/x/Projects/foo\n"
	if stdout != wantOut {
		t.Errorf("stdout = %q, want %q", stdout, wantOut)
	}
	if len(tmuxRunner.Calls) != 0 {
		t.Errorf("tmux Calls = %+v, want none — plain fallback never touches tmux", tmuxRunner.Calls)
	}
}

// TestLs_ListenMode_PollerCancelledBeforeAttachOrRecreate is a regression
// test for a bug where Ls stopped the reload poller via "defer cancel()",
// so it kept firing curl against fzf's (already-exited) --listen socket
// for the entire duration of AttachOrRecreate, not just around the
// blocking fzfutil.Run call. It forces listen=true (fzf/curl both
// "available", fzf --help reporting --listen support), makes the fzf
// invocation itself (the "sh -c" call Run shells out through) block for
// over a poller tick so the poller fires at least once, then makes
// AttachOrRecreate's final "attach-session" tmux call also block for over
// a poller tick. If the poller isn't cancelled until Ls returns (the old
// bug), a curl call lands inside the attach-session window; if it's
// cancelled right after Run returns (the fix), none does.
func TestLs_ListenMode_PollerCancelledBeforeAttachOrRecreate(t *testing.T) {
	setStatusHome(t)
	forceFzfAvailable(t)
	forceCurlAvailableForLs(t)

	var mu sync.Mutex
	var curlTimes []time.Time
	var attachStart, attachEnd time.Time

	tmuxRunner := &executil.FakeRunner{
		Responses: func(call executil.Call) (string, string, error) {
			switch call.Name {
			case "fzf":
				return "    --listen=[addr]  Start HTTP server\n", "", nil
			case "sh":
				// This is fzfutil.Run's "sh -c 'fzf ...' < rowsfile" call.
				// Block long enough for the poller's 1s ticker to fire at
				// least once before the picker "returns" a selection.
				time.Sleep(1200 * time.Millisecond)
				return "myfeature\tsome label\tsome desc\n", "", nil
			case "curl":
				mu.Lock()
				curlTimes = append(curlTimes, time.Now())
				mu.Unlock()
				return "", "", nil
			case "tmux":
				if len(call.Args) > 0 && call.Args[0] == "list-sessions" {
					return "$3:myfeature\n", "", nil
				}
				if len(call.Args) > 0 && call.Args[0] == "attach-session" {
					attachStart = time.Now()
					time.Sleep(1200 * time.Millisecond)
					attachEnd = time.Now()
					return "", "", nil
				}
			}
			return "", "", nil
		},
	}

	gitRunner := &executil.FakeRunner{}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{
		{
			Name:  "myfeature",
			Repos: []state.Repo{{RepoPath: "/Users/x/Projects/foo", WorktreePath: t.TempDir()}},
		},
	}

	if err := Ls(d, "/usr/local/bin/mt", "/tmp", false); err != nil {
		t.Fatalf("Ls returned error: %v", err)
	}

	if attachStart.IsZero() {
		t.Fatalf("AttachOrRecreate's attach-session was never called")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(curlTimes) == 0 {
		t.Fatalf("poller never fired a curl call — test didn't give it enough time to tick")
	}
	for _, ct := range curlTimes {
		if ct.After(attachStart) && ct.Before(attachEnd) {
			t.Errorf("poller fired a curl call at %v, inside AttachOrRecreate's attach-session window [%v, %v] — reload poller must be cancelled before AttachOrRecreate runs, not deferred to Ls's return", ct, attachStart, attachEnd)
		}
	}
}

func TestAttachOrRecreate_LiveSession_JustAttaches(t *testing.T) {
	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$3:myfeature\n"}, // list-sessions -> session is live
			{},                         // attach-session
		},
	}
	d := newDeps(gitRunner, tmuxRunner)

	if err := AttachOrRecreate(d, "myfeature", false); err != nil {
		t.Fatalf("AttachOrRecreate returned error: %v", err)
	}

	wantTmuxCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"attach-session", "-t", "$3"}},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v (live session must just attach, no rebuild)", tmuxRunner.Calls, wantTmuxCalls)
	}
}

func TestAttachOrRecreate_DeadSession_RebuildsWindowsPruningMissingWorktrees(t *testing.T) {
	setClaudeHome(t)

	missingWorktree := filepath.Join(t.TempDir(), "gone")
	survivingWorktree := t.TempDir()
	survivingRepoPath := "/Users/x/Projects/bar"
	missingRepoPath := "/Users/x/Projects/gone-repo"

	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("no server running")}, // list-sessions (top-level FindSessionID) -> ""
			{Err: errors.New("no server running")}, // list-sessions (inside ensureSessionWindow) -> ""
			{Stdout: "$5\n"},                       // new-session -> sessionID
			{},                                     // split-window -h
			{},                                     // split-window -v
			{},                                     // send-keys nvim
			{},                                     // send-keys claude resume
			{},                                     // attach-session
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{
		{
			Name: "myfeature",
			Repos: []state.Repo{
				{RepoPath: missingRepoPath, WorktreePath: missingWorktree},
				{RepoPath: survivingRepoPath, WorktreePath: survivingWorktree},
			},
		},
	}

	var err error
	stderr := captureStderr(t, func() {
		err = AttachOrRecreate(d, "myfeature", false)
	})
	if err != nil {
		t.Fatalf("AttachOrRecreate returned error: %v", err)
	}

	wantWarning := "mt: skipping '" + missingRepoPath + "' — worktree is gone, pruning it from tracking\n"
	if stderr != wantWarning {
		t.Errorf("stderr = %q, want %q", stderr, wantWarning)
	}

	remaining := d.State.ReposFor("myfeature")
	if len(remaining) != 1 || remaining[0].RepoPath != survivingRepoPath {
		t.Errorf("ReposFor(myfeature) = %+v, want only the surviving repo %q", remaining, survivingRepoPath)
	}

	windowTarget := "$5:bar"
	wantTmuxCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"new-session", "-d", "-s", "myfeature", "-n", "bar", "-c", survivingWorktree, "-P", "-F", "#{session_id}"}},
		{Name: "tmux", Args: []string{"split-window", "-h", "-p", "25", "-t", windowTarget, "-c", survivingWorktree}},
		{Name: "tmux", Args: []string{"split-window", "-v", "-t", windowTarget + ".1", "-c", survivingWorktree}},
		{Name: "tmux", Args: []string{"send-keys", "-t", windowTarget + ".0", "nvim .", "C-m"}},
		{Name: "tmux", Args: []string{"send-keys", "-t", windowTarget + ".2", "claude", "C-m"}},
		{Name: "tmux", Args: []string{"attach-session", "-t", "$5"}},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v", tmuxRunner.Calls, wantTmuxCalls)
	}
}

func TestAttachOrRecreate_NoRepoWorktreesLeft_ReturnsError(t *testing.T) {
	missingWorktree := filepath.Join(t.TempDir(), "gone")

	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("no server running")}, // list-sessions -> ""
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{
		{
			Name:  "myfeature",
			Repos: []state.Repo{{RepoPath: "/Users/x/Projects/gone-repo", WorktreePath: missingWorktree}},
		},
	}

	err := captureStderrAndReturnErr(t, func() error { return AttachOrRecreate(d, "myfeature", false) })
	if err == nil {
		t.Fatalf("AttachOrRecreate returned nil error, want an error — no worktree survived")
	}
	wantErr := "mt: nothing left to recreate for session 'myfeature'"
	if err.Error() != wantErr {
		t.Errorf("AttachOrRecreate error = %q, want %q", err.Error(), wantErr)
	}

	if len(tmuxRunner.Calls) != 1 {
		t.Errorf("tmux Calls = %+v, want exactly 1 (the initial FindSessionID check, no rebuild attempt)", tmuxRunner.Calls)
	}
	if len(d.State.ReposFor("myfeature")) != 0 {
		t.Errorf("ReposFor(myfeature) = %+v, want none — the only repo's worktree was gone", d.State.ReposFor("myfeature"))
	}
}

// captureStderrAndReturnErr runs fn with stderr redirected (and discarded)
// so its pruning-warning side effect doesn't pollute test output, and
// returns fn's error.
func captureStderrAndReturnErr(t *testing.T, fn func() error) error {
	t.Helper()
	var err error
	captureStderr(t, func() { err = fn() })
	return err
}
