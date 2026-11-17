package mtcmd

import (
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/mauricebutts/motree/internal/executil"
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

func TestSessionStatus_AnyLiveSubagent_ShowsSubagentsEvenOverRunning(t *testing.T) {
	setStatusHome(t)

	worktree := t.TempDir()
	repos := []state.Repo{{RepoPath: "/repo/running", WorktreePath: worktree}}

	if err := statusfile.Write(worktree, statusfile.Running); err != nil {
		t.Fatalf("Write running: %v", err)
	}
	if err := statusfile.WriteSubagent(worktree, "agent-1", statusfile.Running); err != nil {
		t.Fatalf("WriteSubagent: %v", err)
	}

	status, found := SessionStatus(repos)
	if !found || status != statusfile.Subagents {
		t.Errorf("SessionStatus = (%q, %v), want (%q, true) — any live subagent must win over the parent's own plain Running", status, found, statusfile.Subagents)
	}
}

func TestSubagentLine_JoinsOneEmojiPerLiveSubagent(t *testing.T) {
	setStatusHome(t)

	worktree := t.TempDir()
	repos := []state.Repo{{RepoPath: "/repo/foo", WorktreePath: worktree}}

	if err := statusfile.WriteSubagent(worktree, "agent-1", statusfile.Running); err != nil {
		t.Fatalf("WriteSubagent: %v", err)
	}
	if err := statusfile.WriteSubagent(worktree, "agent-2", statusfile.Waiting); err != nil {
		t.Fatalf("WriteSubagent: %v", err)
	}

	line, found := SubagentLine(repos)
	if !found {
		t.Fatalf("SubagentLine found = false, want true")
	}

	wantEmoji := map[rune]bool{'🏃': false, '🔔': false}
	for _, r := range line {
		if _, ok := wantEmoji[r]; ok {
			wantEmoji[r] = true
		}
	}
	for emoji, seen := range wantEmoji {
		if !seen {
			t.Errorf("SubagentLine = %q, want it to contain %q", line, string(emoji))
		}
	}
	if line[:len(subagentIndent)] != subagentIndent {
		t.Errorf("SubagentLine = %q, want it to start with the indent %q", line, subagentIndent)
	}
}

func TestSubagentLine_NoLiveSubagents_NotFound(t *testing.T) {
	setStatusHome(t)

	repos := []state.Repo{{RepoPath: "/repo/foo", WorktreePath: t.TempDir()}}

	line, found := SubagentLine(repos)
	if found || line != "" {
		t.Errorf("SubagentLine = (%q, %v), want (\"\", false)", line, found)
	}
}

func TestRenderRows_EmbedsSubagentLineInSameRowAsParent(t *testing.T) {
	setStatusHome(t)

	worktree := t.TempDir()
	st := &state.State{
		Sessions: []state.Session{
			{Name: "myfeature", Description: "d1", LastOpened: 100, Repos: []state.Repo{{RepoPath: "/r/foo", WorktreePath: worktree}}},
		},
	}
	if err := statusfile.Write(worktree, statusfile.Running); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := statusfile.WriteSubagent(worktree, "agent-1", statusfile.Running); err != nil {
		t.Fatalf("WriteSubagent: %v", err)
	}

	rows := RenderRows(st)
	if len(rows) != 1 {
		t.Fatalf("RenderRows returned %d rows, want 1 — the subagent line must stay part of the parent's own row (an embedded \"\\n\"), not a separate row: %v", len(rows), rows)
	}

	wantLabel := FormatLabel(st.Sessions[0], statusfile.Subagents, true) + "\n" + subagentIndent + statusEmoji[statusfile.Running]
	wantRow := "myfeature\t" + wantLabel + "\td1"
	if rows[0] != wantRow {
		t.Errorf("rows[0] = %q, want %q", rows[0], wantRow)
	}
}

// TestRenderRows_IncludeSubagentsFalse_OmitsSubagentLine pins mt rm's use
// of renderRows(st, false): even when a session has live subagents, no
// "\n"-embedded subagent line is appended, and every row stays a plain
// single line — mt rm's picker has no --read0/--print0 multi-line item
// support to rely on (see rmFzfArgs), and no way to act on a subagent
// anyway.
func TestRenderRows_IncludeSubagentsFalse_OmitsSubagentLine(t *testing.T) {
	setStatusHome(t)

	worktree := t.TempDir()
	st := &state.State{
		Sessions: []state.Session{
			{Name: "myfeature", Description: "d1", LastOpened: 100, Repos: []state.Repo{{RepoPath: "/r/foo", WorktreePath: worktree}}},
		},
	}
	if err := statusfile.Write(worktree, statusfile.Running); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := statusfile.WriteSubagent(worktree, "agent-1", statusfile.Running); err != nil {
		t.Fatalf("WriteSubagent: %v", err)
	}

	rows := renderRows(st, false)
	if len(rows) != 1 {
		t.Fatalf("renderRows(st, false) returned %d rows, want 1: %v", len(rows), rows)
	}

	wantLabel := FormatLabel(st.Sessions[0], statusfile.Subagents, true)
	wantRow := "myfeature\t" + wantLabel + "\td1"
	if rows[0] != wantRow {
		t.Errorf("rows[0] = %q, want %q (no embedded subagent line)", rows[0], wantRow)
	}
}

func TestRenderRows_NoSubagents_NoExtraRow(t *testing.T) {
	setStatusHome(t)

	st := &state.State{
		Sessions: []state.Session{
			{Name: "myfeature", Description: "d1", LastOpened: 100, Repos: []state.Repo{{RepoPath: "/r/foo", WorktreePath: t.TempDir()}}},
		},
	}

	rows := RenderRows(st)
	if len(rows) != 1 {
		t.Fatalf("RenderRows returned %d rows, want 1 (no subagents tracked): %v", len(rows), rows)
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
// so it kept firing a reload request against fzf's (already-exited)
// --listen socket for the entire duration of AttachOrRecreate, not just
// around the blocking fzfutil.Run call. The poller talks to that socket
// directly over HTTP (no curl, no executil.Runner involved — see
// fzfutil.StartReloadPoller), so this test stands up a real Unix-socket
// HTTP listener at a path it controls (overriding newFzfSocketPath) to
// observe real requests landing, rather than intercepting a mocked
// subprocess call. It forces listen=true (fzf "available", its --help
// reporting --listen support), makes the fzf invocation itself (the
// "sh -c" call Run shells out through) block for over a poller tick so
// the poller fires at least once, then makes AttachOrRecreate's final
// "attach-session" tmux call also block for over a poller tick. If the
// poller isn't cancelled until Ls returns (the old bug), a reload request
// lands inside the attach-session window; if it's cancelled right after
// Run returns (the fix), none does.
func TestLs_ListenMode_PollerCancelledBeforeAttachOrRecreate(t *testing.T) {
	setStatusHome(t)
	forceFzfAvailable(t)

	// A short path under the OS temp root, not t.TempDir() (which nests
	// under a directory named after the test function and routinely
	// exceeds the ~104-byte sun_path limit on macOS).
	socketFile, err := os.CreateTemp("", "mt-fzf-test-*.sock")
	if err != nil {
		t.Fatalf("os.CreateTemp: %v", err)
	}
	socketPath := socketFile.Name()
	socketFile.Close()
	if err := os.Remove(socketPath); err != nil {
		t.Fatalf("os.Remove(%q): %v", socketPath, err)
	}
	t.Cleanup(func() { os.Remove(socketPath) })

	origSocketPath := newFzfSocketPath
	newFzfSocketPath = func() (string, error) { return socketPath, nil }
	t.Cleanup(func() { newFzfSocketPath = origSocketPath })

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen(unix, %q): %v", socketPath, err)
	}
	defer listener.Close()

	var mu sync.Mutex
	var reloadTimes []time.Time
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			reloadTimes = append(reloadTimes, time.Now())
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}),
	}
	go server.Serve(listener)
	defer server.Close()

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
	if len(reloadTimes) == 0 {
		t.Fatalf("poller never fired a reload request — test didn't give it enough time to tick")
	}
	for _, rt := range reloadTimes {
		if rt.After(attachStart) && rt.Before(attachEnd) {
			t.Errorf("poller fired a reload request at %v, inside AttachOrRecreate's attach-session window [%v, %v] — reload poller must be cancelled before AttachOrRecreate runs, not deferred to Ls's return", rt, attachStart, attachEnd)
		}
	}
}

func TestAttachOrRecreate_LiveSession_JustAttaches(t *testing.T) {
	setStatusHome(t)

	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$3:myfeature\n"}, // list-sessions -> session is live
			{},                         // attach-session
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{{Name: "myfeature", Description: "fix the thing", LastOpened: 100}}

	if err := AttachOrRecreate(d, "myfeature", false, 4242); err != nil {
		t.Fatalf("AttachOrRecreate returned error: %v", err)
	}

	// attach-session must go through RunInteractive (Interactive: true),
	// not the captured-buffer Run every other tmux call uses: tmux needs a
	// real tty on its stdin or it dies with "open terminal failed: not a
	// terminal".
	wantTmuxCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"attach-session", "-t", "$3"}, Interactive: true},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v (live session must just attach, no rebuild)", tmuxRunner.Calls, wantTmuxCalls)
	}

	if got := d.State.Sessions[0].LastOpened; got != 4242 {
		t.Errorf("LastOpened = %d, want 4242 — attaching to a live session must bump it", got)
	}
	if got := d.State.Sessions[0].Description; got != "fix the thing" {
		t.Errorf("Description = %q, want it preserved by the empty-description upsert", got)
	}

	// And it must already be on disk by the time we attach, since attaching
	// hands the terminal (and possibly this process's life) to tmux.
	saved := readSavedState(t)
	if len(saved.Sessions) != 1 || saved.Sessions[0].LastOpened != 4242 {
		t.Errorf("saved state = %+v, want the bumped LastOpened persisted before attach", saved.Sessions)
	}
}

// TestAttachOrRecreate_SavesStateBeforeAttaching pins the ordering that
// finding #1/#2 turn on: the state file must already contain the bump by
// the time the attach-session call is issued, because attaching takes the
// terminal over and anything cmd/mt/main.go would save afterwards may never
// run.
func TestAttachOrRecreate_SavesStateBeforeAttaching(t *testing.T) {
	setStatusHome(t)

	var savedAtAttach *state.State
	tmuxRunner := &executil.FakeRunner{
		Responses: func(call executil.Call) (string, string, error) {
			if len(call.Args) > 0 && call.Args[0] == "list-sessions" {
				return "$3:myfeature\n", "", nil
			}
			if len(call.Args) > 0 && call.Args[0] == "attach-session" {
				st, err := state.Load(state.StateFilePath())
				if err == nil {
					savedAtAttach = st
				}
			}
			return "", "", nil
		},
	}
	d := newDeps(&executil.FakeRunner{}, tmuxRunner)
	d.State.Sessions = []state.Session{{Name: "myfeature", LastOpened: 100}}

	if err := AttachOrRecreate(d, "myfeature", false, 777); err != nil {
		t.Fatalf("AttachOrRecreate returned error: %v", err)
	}

	if savedAtAttach == nil {
		t.Fatalf("state file did not exist yet when attach-session ran — Save must happen first")
	}
	if len(savedAtAttach.Sessions) != 1 || savedAtAttach.Sessions[0].LastOpened != 777 {
		t.Errorf("state on disk at attach time = %+v, want LastOpened 777 already persisted", savedAtAttach.Sessions)
	}
}

func TestAttachOrRecreate_DeadSession_RebuildsWindowsPruningMissingWorktrees(t *testing.T) {
	setClaudeHome(t)
	setStatusHome(t)

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
		err = AttachOrRecreate(d, "myfeature", false, 9999)
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
		{Name: "tmux", Args: []string{"attach-session", "-t", "$5"}, Interactive: true},
	}
	if !reflect.DeepEqual(tmuxRunner.Calls, wantTmuxCalls) {
		t.Errorf("tmux Calls = %+v, want %+v", tmuxRunner.Calls, wantTmuxCalls)
	}

	sess := d.State.Sessions[0]
	if sess.LastOpened != 9999 {
		t.Errorf("LastOpened = %d, want 9999 — the rebuild branch must bump it too", sess.LastOpened)
	}
	saved := readSavedState(t)
	if len(saved.Sessions) != 1 || saved.Sessions[0].LastOpened != 9999 || len(saved.Sessions[0].Repos) != 1 {
		t.Errorf("saved state = %+v, want the bump and the pruned repo list persisted before attach", saved.Sessions)
	}
}

// TestAttachOrRecreate_DeadSession_ThreeRepos_VisitsEverySurvivorExactlyOnce
// is the regression test for the slice-aliasing bug: ReposFor used to hand
// back the session's own backing slice, and RemoveRepo (called from inside
// this loop for repos whose worktree is gone) rewrites that slice in place.
// With the FIRST of three repos pruned, the loop's view shifted underneath
// it — the second repo was skipped entirely and the third processed twice.
// Two-repo fixtures happen not to expose this, hence three here.
func TestAttachOrRecreate_DeadSession_ThreeRepos_VisitsEverySurvivorExactlyOnce(t *testing.T) {
	setClaudeHome(t)
	setStatusHome(t)

	missingWorktree := filepath.Join(t.TempDir(), "gone")
	aliceWorktree := t.TempDir()
	bobWorktree := t.TempDir()

	gitRunner := &executil.FakeRunner{}
	tmuxRunner := &executil.FakeRunner{
		Responses: func(call executil.Call) (string, string, error) {
			if call.Name != "tmux" || len(call.Args) == 0 {
				return "", "", nil
			}
			switch call.Args[0] {
			case "list-sessions":
				return "", "", errors.New("no server running")
			case "new-session":
				return "$7\n", "", nil
			}
			return "", "", nil
		},
	}
	d := newDeps(gitRunner, tmuxRunner)
	d.State.Sessions = []state.Session{
		{
			Name: "myfeature",
			Repos: []state.Repo{
				{RepoPath: "/Users/x/Projects/gone-repo", WorktreePath: missingWorktree},
				{RepoPath: "/Users/x/Projects/alice", WorktreePath: aliceWorktree},
				{RepoPath: "/Users/x/Projects/bob", WorktreePath: bobWorktree},
			},
		},
	}

	var err error
	captureStderr(t, func() { err = AttachOrRecreate(d, "myfeature", false, 1) })
	if err != nil {
		t.Fatalf("AttachOrRecreate returned error: %v", err)
	}

	// Each surviving repo must get exactly one window: "alice" as the new
	// session's initial window, "bob" via new-window. The old bug produced
	// "alice" twice and "bob" never.
	var windowsBuilt []string
	for _, call := range tmuxRunner.Calls {
		if call.Name != "tmux" || len(call.Args) == 0 {
			continue
		}
		switch call.Args[0] {
		case "new-session":
			windowsBuilt = append(windowsBuilt, call.Args[5]) // -n <window>
		case "new-window":
			windowsBuilt = append(windowsBuilt, call.Args[4]) // -n <window>
		}
	}
	wantWindows := []string{"alice", "bob"}
	if !reflect.DeepEqual(windowsBuilt, wantWindows) {
		t.Errorf("windows built = %v, want %v (every surviving repo visited exactly once)", windowsBuilt, wantWindows)
	}

	remaining := d.State.ReposFor("myfeature")
	if len(remaining) != 2 || remaining[0].RepoPath != "/Users/x/Projects/alice" || remaining[1].RepoPath != "/Users/x/Projects/bob" {
		t.Errorf("ReposFor(myfeature) = %+v, want exactly the two surviving repos", remaining)
	}
}

// TestReposFor_ResultIsNotAliasedToStateBackingSlice is the unit-level
// counterpart to the regression test above: mutating state through
// RemoveRepo must not disturb a slice ReposFor already handed out.
func TestReposFor_ResultIsNotAliasedToStateBackingSlice(t *testing.T) {
	st := &state.State{
		Sessions: []state.Session{
			{
				Name: "myfeature",
				Repos: []state.Repo{
					{RepoPath: "/r/a"}, {RepoPath: "/r/b"}, {RepoPath: "/r/c"},
				},
			},
		},
	}

	snapshot := st.ReposFor("myfeature")
	st.RemoveRepo("myfeature", "/r/a")

	want := []string{"/r/a", "/r/b", "/r/c"}
	if len(snapshot) != len(want) {
		t.Fatalf("snapshot length = %d, want %d — ReposFor must return a copy", len(snapshot), len(want))
	}
	for i, w := range want {
		if snapshot[i].RepoPath != w {
			t.Errorf("snapshot[%d] = %q, want %q — RemoveRepo must not rewrite an already-returned slice", i, snapshot[i].RepoPath, w)
		}
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

	err := captureStderrAndReturnErr(t, func() error { return AttachOrRecreate(d, "myfeature", false, 1) })
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
