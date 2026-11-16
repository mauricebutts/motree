package mtcmd

import (
	"path/filepath"
	"testing"

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
	if len(tmuxRunner.Calls) != len(wantTmuxCalls) {
		t.Fatalf("tmux Calls = %+v, want %+v", tmuxRunner.Calls, wantTmuxCalls)
	}
	for i, c := range wantTmuxCalls {
		if tmuxRunner.Calls[i].Name != c.Name {
			t.Errorf("Calls[%d].Name = %q, want %q", i, tmuxRunner.Calls[i].Name, c.Name)
		}
	}
}

// timeFormatForTest mirrors FormatLabel's exact date formatting so the test
// doesn't hardcode a timezone-dependent string.
func timeFormatForTest(ts int64) string {
	return formatTimeForLabel(ts)
}
