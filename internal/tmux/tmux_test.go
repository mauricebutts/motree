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

func TestAttachOrSwitch_UsesAttachSessionWhenNotInsideTmux(t *testing.T) {
	fake := &executil.FakeRunner{Queue: []executil.Response{{}}}
	tm := Tmux{Runner: fake}

	if err := tm.AttachOrSwitch("$3", false); err != nil {
		t.Fatalf("AttachOrSwitch returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"attach-session", "-t", "$3"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}
