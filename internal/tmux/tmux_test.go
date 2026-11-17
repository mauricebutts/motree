package tmux

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mauricebutts/motree/internal/executil"
)

func TestQuote_EscapesSingleQuotesSpacesDollarBackticks(t *testing.T) {
	in := `it's $HOME ` + "`whoami`" + ` "two words"`
	want := `'it'\''s $HOME ` + "`whoami`" + ` "two words"'`

	got := Quote(in)
	if got != want {
		t.Errorf("Quote(%q) = %q, want %q", in, got, want)
	}
}

func TestFindSessionID_MatchesByNameIgnoringDotsInOtherNames(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$0:feature.fix\n$1:target\n$2:other\n"},
		},
	}
	tm := Tmux{Runner: fake}

	got, err := tm.FindSessionID("target")
	if err != nil {
		t.Fatalf("FindSessionID returned error: %v", err)
	}
	if got != "$1" {
		t.Errorf("FindSessionID(%q) = %q, want %q", "target", got, "$1")
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestFindSessionID_NotFound_ReturnsEmpty(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$0:feature.fix\n$2:other\n"},
		},
	}
	tm := Tmux{Runner: fake}

	got, err := tm.FindSessionID("target")
	if err != nil {
		t.Fatalf("FindSessionID returned error: %v", err)
	}
	if got != "" {
		t.Errorf("FindSessionID(%q) = %q, want empty string", "target", got)
	}
}

func TestFindSessionID_NoTmuxServer_ReturnsEmptyNoError(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("no server running")},
		},
	}
	tm := Tmux{Runner: fake}

	got, err := tm.FindSessionID("target")
	if err != nil {
		t.Fatalf("FindSessionID returned error: %v, want nil (swallowed, matching bash's 2>/dev/null)", err)
	}
	if got != "" {
		t.Errorf("FindSessionID(%q) = %q, want empty string", "target", got)
	}
}

func TestNewSession_IssuesExactArgvWithOptionalSize(t *testing.T) {
	t.Run("without size", func(t *testing.T) {
		fake := &executil.FakeRunner{
			Queue: []executil.Response{{Stdout: "$3\n"}},
		}
		tm := Tmux{Runner: fake}

		got, err := tm.NewSession("mysess", "myrepo", "/cwd", nil)
		if err != nil {
			t.Fatalf("NewSession returned error: %v", err)
		}
		if got != "$3" {
			t.Errorf("NewSession() = %q, want %q", got, "$3")
		}

		wantCalls := []executil.Call{
			{Name: "tmux", Args: []string{
				"new-session", "-d", "-s", "mysess", "-n", "myrepo", "-c", "/cwd",
				"-P", "-F", "#{session_id}",
			}},
		}
		if !reflect.DeepEqual(fake.Calls, wantCalls) {
			t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
		}
	})

	t.Run("with size", func(t *testing.T) {
		fake := &executil.FakeRunner{
			Queue: []executil.Response{{Stdout: "$4\n"}},
		}
		tm := Tmux{Runner: fake}

		got, err := tm.NewSession("mysess", "myrepo", "/cwd", &WindowSize{X: 200, Y: 50})
		if err != nil {
			t.Fatalf("NewSession returned error: %v", err)
		}
		if got != "$4" {
			t.Errorf("NewSession() = %q, want %q", got, "$4")
		}

		wantCalls := []executil.Call{
			{Name: "tmux", Args: []string{
				"new-session", "-d", "-s", "mysess", "-n", "myrepo", "-c", "/cwd",
				"-x", "200", "-y", "50",
				"-P", "-F", "#{session_id}",
			}},
		}
		if !reflect.DeepEqual(fake.Calls, wantCalls) {
			t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
		}
	})
}

func TestNewWindow_IssuesExactArgv(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{}}}
	tm := Tmux{Runner: fake}

	if err := tm.NewWindow("$3", "myrepo", "/cwd"); err != nil {
		t.Fatalf("NewWindow returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"new-window", "-t", "$3", "-n", "myrepo", "-c", "/cwd"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestWindowExists_TrueWhenNameAmongListedWindows(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{{Stdout: "other\nmyrepo\n"}},
	}
	tm := Tmux{Runner: fake}

	got, err := tm.WindowExists("$3", "myrepo")
	if err != nil {
		t.Fatalf("WindowExists returned error: %v", err)
	}
	if !got {
		t.Errorf("WindowExists() = false, want true")
	}
}

func TestWindowExists_FalseWhenNameNotListed(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{{Stdout: "other\n"}},
	}
	tm := Tmux{Runner: fake}

	got, err := tm.WindowExists("$3", "myrepo")
	if err != nil {
		t.Fatalf("WindowExists returned error: %v", err)
	}
	if got {
		t.Errorf("WindowExists() = true, want false")
	}
}

func TestSplitThreePane_IssuesExpectedSplitArgvSequence(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{{}, {}},
	}
	tm := Tmux{Runner: fake}

	if err := tm.SplitThreePane("$3:myrepo", "/cwd"); err != nil {
		t.Fatalf("SplitThreePane returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"split-window", "-h", "-p", "25", "-t", "$3:myrepo", "-c", "/cwd"}},
		{Name: "tmux", Args: []string{"split-window", "-v", "-t", "$3:myrepo.1", "-c", "/cwd"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestSendKeys_IssuesExactArgvWithEnterKey(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{}}}
	tm := Tmux{Runner: fake}

	if err := tm.SendKeys("$3:myrepo.2", "nvim ."); err != nil {
		t.Fatalf("SendKeys returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"send-keys", "-t", "$3:myrepo.2", "nvim .", "C-m"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestFindClaudePane_ReturnsFirstPaneRunningClaude(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{{Stdout: "0 nvim\n1 zsh\n2 claude\n"}},
	}
	tm := Tmux{Runner: fake}

	pane, found, err := tm.FindClaudePane("$3", "myrepo")
	if err != nil {
		t.Fatalf("FindClaudePane returned error: %v", err)
	}
	if !found {
		t.Fatalf("FindClaudePane found = false, want true")
	}
	if pane != "2" {
		t.Errorf("FindClaudePane pane = %q, want %q", pane, "2")
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-panes", "-t", "$3:myrepo", "-F", "#{pane_index} #{pane_current_command}"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestFindClaudePane_NoClaudePane_ReturnsNotFound(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{{Stdout: "0 nvim\n1 zsh\n"}},
	}
	tm := Tmux{Runner: fake}

	pane, found, err := tm.FindClaudePane("$3", "myrepo")
	if err != nil {
		t.Fatalf("FindClaudePane returned error: %v", err)
	}
	if found {
		t.Errorf("FindClaudePane found = true, want false")
	}
	if pane != "" {
		t.Errorf("FindClaudePane pane = %q, want empty string", pane)
	}
}

func TestCapturePane_ReturnsLastNLinesOfPaneText(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{{Stdout: "line1\nline2\nline3\nline4\nline5\n"}},
	}
	tm := Tmux{Runner: fake}

	got, err := tm.CapturePane("$3:myrepo.2", 2)
	if err != nil {
		t.Fatalf("CapturePane returned error: %v", err)
	}
	if got != "line4\nline5" {
		t.Errorf("CapturePane() = %q, want %q", got, "line4\nline5")
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"capture-pane", "-t", "$3:myrepo.2", "-p"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestKillSession_IssuesExactArgv(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{}}}
	tm := Tmux{Runner: fake}

	if err := tm.KillSession("$3"); err != nil {
		t.Fatalf("KillSession returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"kill-session", "-t", "$3"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestKillSession_PropagatesError(t *testing.T) {
	wantErr := errors.New("boom")
	fake := &executil.FakeRunner{Queue: []executil.Response{{Err: wantErr}}}
	tm := Tmux{Runner: fake}

	err := tm.KillSession("$3")
	if !errors.Is(err, wantErr) {
		t.Errorf("KillSession error = %v, want %v", err, wantErr)
	}
}

func TestAttachOrSwitch_UsesSwitchClientWhenInsideTmux(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{}}}
	tm := Tmux{Runner: fake}

	if err := tm.AttachOrSwitch("$3", true); err != nil {
		t.Fatalf("AttachOrSwitch returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"switch-client", "-t", "$3"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

// TestAttachOrSwitch_UsesAttachSessionWhenNotInsideTmux also pins which
// Runner method the attach branch uses. "tmux attach-session" needs its
// stdin to be a real terminal; routed through the ordinary Run (captured
// buffers, nil stdin) it fails with "open terminal failed: not a terminal",
// so `mt <name>`, `mt pr` and `mt ls` could never attach from outside tmux.
// Interactive: true is the proof it goes through RunInteractive, which
// inherits this process's real stdio. (A unit test can't supply a real tty,
// so the end of that path — RealRunner.RunInteractive handing over an
// actual terminal — is only verifiable by running the built binary.)
func TestAttachOrSwitch_UsesAttachSessionWhenNotInsideTmux(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{}}}
	tm := Tmux{Runner: fake}

	if err := tm.AttachOrSwitch("$3", false); err != nil {
		t.Fatalf("AttachOrSwitch returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"attach-session", "-t", "$3"}, Interactive: true},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v (attach-session must use RunInteractive, not Run)", fake.Calls, wantCalls)
	}
}

func TestAttachOrSwitch_AttachSessionErrorPropagates(t *testing.T) {
	wantErr := errors.New("open terminal failed: not a terminal")
	fake := &executil.FakeRunner{Queue: []executil.Response{{Err: wantErr}}}
	tm := Tmux{Runner: fake}

	if err := tm.AttachOrSwitch("$3", false); !errors.Is(err, wantErr) {
		t.Errorf("AttachOrSwitch error = %v, want %v", err, wantErr)
	}
}

func TestDisplayMessage_ReturnsTrimmedStdout(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{Stdout: "/dev/ttys003\n"}}}
	tm := Tmux{Runner: fake}

	got, err := tm.DisplayMessage("$3:cmd.1", "#{pane_tty}")
	if err != nil {
		t.Fatalf("DisplayMessage returned error: %v", err)
	}
	if got != "/dev/ttys003" {
		t.Errorf("DisplayMessage() = %q, want %q", got, "/dev/ttys003")
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"display-message", "-p", "-t", "$3:cmd.1", "#{pane_tty}"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestDisplayMessageCurrent_NoDashT(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{Stdout: "myname\n"}}}
	tm := Tmux{Runner: fake}

	got, err := tm.DisplayMessageCurrent("#S")
	if err != nil {
		t.Fatalf("DisplayMessageCurrent returned error: %v", err)
	}
	if got != "myname" {
		t.Errorf("DisplayMessageCurrent() = %q, want %q", got, "myname")
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"display-message", "-p", "#S"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestShowMessage_NoDashP(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{}}}
	tm := Tmux{Runner: fake}

	if err := tm.ShowMessage("$3:cmd.1", "mt cmd: no claude pane running for 'foo'"); err != nil {
		t.Fatalf("ShowMessage returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"display-message", "-t", "$3:cmd.1", "mt cmd: no claude pane running for 'foo'"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestSplitRightPane_BuildsExpectedArgv(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{}}}
	tm := Tmux{Runner: fake}

	if err := tm.SplitRightPane("$3:cmd", "/home/user", 70); err != nil {
		t.Fatalf("SplitRightPane returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"split-window", "-h", "-p", "70", "-t", "$3:cmd", "-c", "/home/user"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestSessionOption_ReturnsEmptyStringWhenUnset(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{Stdout: "\n"}}}
	tm := Tmux{Runner: fake}

	got, err := tm.SessionOption("$3", "@mt_cmd_docked")
	if err != nil {
		t.Fatalf("SessionOption returned error: %v", err)
	}
	if got != "" {
		t.Errorf("SessionOption() = %q, want empty string", got)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"show-option", "-t", "$3", "-v", "@mt_cmd_docked"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestSetSessionOption_BuildsExpectedArgv(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{}}}
	tm := Tmux{Runner: fake}

	if err := tm.SetSessionOption("$3", "@mt_cmd_docked", "feature-x"); err != nil {
		t.Fatalf("SetSessionOption returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"set-option", "-t", "$3", "@mt_cmd_docked", "feature-x"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestListClientSessions_ParsesTtySessionIDPairs(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{{Stdout: "/dev/ttys001 $2\n/dev/ttys003 $5\n"}},
	}
	tm := Tmux{Runner: fake}

	got, err := tm.ListClientSessions()
	if err != nil {
		t.Fatalf("ListClientSessions returned error: %v", err)
	}
	want := map[string]string{"/dev/ttys001": "$2", "/dev/ttys003": "$5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListClientSessions() = %+v, want %+v", got, want)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-clients", "-F", "#{client_tty} #{session_id}"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestListClientSessions_EmptyOnNoClients(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{{Err: errors.New("no server running")}},
	}
	tm := Tmux{Runner: fake}

	got, err := tm.ListClientSessions()
	if err != nil {
		t.Fatalf("ListClientSessions returned error: %v, want nil (swallowed)", err)
	}
	if len(got) != 0 {
		t.Errorf("ListClientSessions() = %+v, want empty map", got)
	}
}

func TestSwitchClientTo_BuildsExpectedArgv(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{}}}
	tm := Tmux{Runner: fake}

	if err := tm.SwitchClientTo("/dev/ttys003", "$5"); err != nil {
		t.Fatalf("SwitchClientTo returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"switch-client", "-c", "/dev/ttys003", "-t", "$5"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestSelectWindow_BuildsExpectedArgv(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{}}}
	tm := Tmux{Runner: fake}

	if err := tm.SelectWindow("$3:cmd"); err != nil {
		t.Fatalf("SelectWindow returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"select-window", "-t", "$3:cmd"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestSelectPane_BuildsExpectedArgv(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{}}}
	tm := Tmux{Runner: fake}

	if err := tm.SelectPane("$3:cmd.1"); err != nil {
		t.Fatalf("SelectPane returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"select-pane", "-t", "$3:cmd.1"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestPanePID_ParsesMatchingIndex(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{{Stdout: "0 1234\n1 5678\n"}},
	}
	tm := Tmux{Runner: fake}

	got, err := tm.PanePID("$7:cmd.0")
	if err != nil {
		t.Fatalf("PanePID returned error: %v", err)
	}
	if got != "1234" {
		t.Errorf("PanePID() = %q, want %q", got, "1234")
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-panes", "-t", "$7:cmd", "-F", "#{pane_index} #{pane_pid}"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestPanePID_NoMatch_ReturnsEmpty(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{{Stdout: "1 5678\n"}},
	}
	tm := Tmux{Runner: fake}

	got, err := tm.PanePID("$7:cmd.0")
	if err != nil {
		t.Fatalf("PanePID returned error: %v", err)
	}
	if got != "" {
		t.Errorf("PanePID() = %q, want empty string", got)
	}
}

func TestHasChildProcess_TrueOnZeroExit(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{Stdout: "5678\n"}}}
	tm := Tmux{Runner: fake}

	if got := tm.HasChildProcess("1234"); !got {
		t.Errorf("HasChildProcess() = false, want true")
	}

	wantCalls := []executil.Call{
		{Name: "pgrep", Args: []string{"-P", "1234"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestHasChildProcess_FalseOnError(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{Err: errors.New("exit status 1")}}}
	tm := Tmux{Runner: fake}

	if got := tm.HasChildProcess("1234"); got {
		t.Errorf("HasChildProcess() = true, want false")
	}
}

func TestZoomOff_TogglesOnlyWhenFlagIsOne(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{{Stdout: "1\n"}, {}},
	}
	tm := Tmux{Runner: fake}

	if err := tm.ZoomOff("$3:cmd"); err != nil {
		t.Fatalf("ZoomOff returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"display-message", "-p", "-t", "$3:cmd", "#{window_zoomed_flag}"}},
		{Name: "tmux", Args: []string{"resize-pane", "-Z", "-t", "$3:cmd"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestZoomOff_NoopWhenFlagIsZero(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{{Stdout: "0\n"}},
	}
	tm := Tmux{Runner: fake}

	if err := tm.ZoomOff("$3:cmd"); err != nil {
		t.Fatalf("ZoomOff returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"display-message", "-p", "-t", "$3:cmd", "#{window_zoomed_flag}"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v (no resize-pane call)", fake.Calls, wantCalls)
	}
}

func TestZoomOn_TogglesOnlyWhenFlagIsZero(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{{Stdout: "0\n"}, {}},
	}
	tm := Tmux{Runner: fake}

	if err := tm.ZoomOn("$3:cmd"); err != nil {
		t.Fatalf("ZoomOn returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"display-message", "-p", "-t", "$3:cmd", "#{window_zoomed_flag}"}},
		{Name: "tmux", Args: []string{"resize-pane", "-Z", "-t", "$3:cmd"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestZoomOn_NoopWhenFlagIsOne(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{{Stdout: "1\n"}},
	}
	tm := Tmux{Runner: fake}

	if err := tm.ZoomOn("$3:cmd"); err != nil {
		t.Fatalf("ZoomOn returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"display-message", "-p", "-t", "$3:cmd", "#{window_zoomed_flag}"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v (no resize-pane call)", fake.Calls, wantCalls)
	}
}

func TestZoomOff_NoopOnEmptyTarget(t *testing.T) {
	fake := &executil.FakeRunner{}
	tm := Tmux{Runner: fake}

	if err := tm.ZoomOff(""); err != nil {
		t.Fatalf("ZoomOff returned error: %v", err)
	}
	if len(fake.Calls) != 0 {
		t.Errorf("Calls = %+v, want none", fake.Calls)
	}
}

func TestZoomOn_NoopOnEmptyTarget(t *testing.T) {
	fake := &executil.FakeRunner{}
	tm := Tmux{Runner: fake}

	if err := tm.ZoomOn(""); err != nil {
		t.Fatalf("ZoomOn returned error: %v", err)
	}
	if len(fake.Calls) != 0 {
		t.Errorf("Calls = %+v, want none", fake.Calls)
	}
}
