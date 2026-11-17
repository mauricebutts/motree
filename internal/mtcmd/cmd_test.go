package mtcmd

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mauricebutts/motree/internal/executil"
	"github.com/mauricebutts/motree/internal/state"
	"github.com/mauricebutts/motree/internal/tmux"
)

func TestWindowTargetFor_NoRepos_ReturnsFalse(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$3:feature\n"},
		},
	}
	d := &Deps{
		Tmux: tmux.Tmux{Runner: fake},
		State: &state.State{Sessions: []state.Session{
			{Name: "feature", Repos: []state.Repo{}},
		}},
	}

	target, found := WindowTargetFor(d, "feature")
	if found {
		t.Errorf("WindowTargetFor found = true, want false")
	}
	if target != "" {
		t.Errorf("WindowTargetFor target = %q, want empty string", target)
	}
}

func TestWindowTargetFor_NoLiveSession_ReturnsFalse(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$3:other\n"},
		},
	}
	d := &Deps{
		Tmux: tmux.Tmux{Runner: fake},
		State: &state.State{Sessions: []state.Session{
			{Name: "feature", Repos: []state.Repo{{RepoPath: "/repos/myrepo", WorktreePath: "/wt"}}},
		}},
	}

	target, found := WindowTargetFor(d, "feature")
	if found {
		t.Errorf("WindowTargetFor found = true, want false")
	}
	if target != "" {
		t.Errorf("WindowTargetFor target = %q, want empty string", target)
	}
}

func TestWindowTargetFor_UsesFirstRepoBasename(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$3:feature\n"},
		},
	}
	d := &Deps{
		Tmux: tmux.Tmux{Runner: fake},
		State: &state.State{Sessions: []state.Session{
			{Name: "feature", Repos: []state.Repo{
				{RepoPath: "/repos/myrepo", WorktreePath: "/wt1"},
				{RepoPath: "/repos/otherrepo", WorktreePath: "/wt2"},
			}},
		}},
	}

	target, found := WindowTargetFor(d, "feature")
	if !found {
		t.Fatalf("WindowTargetFor found = false, want true")
	}
	if target != "$3:myrepo" {
		t.Errorf("WindowTargetFor target = %q, want %q", target, "$3:myrepo")
	}
}

func TestDock_EmptyTargetName_ReturnsErrorWithoutTouchingTmux(t *testing.T) {
	fake := &executil.FakeRunner{}
	d := &Deps{Tmux: tmux.Tmux{Runner: fake}, State: &state.State{}}

	if err := Dock(d, ""); err == nil {
		t.Fatal("Dock returned nil error, want an error for an empty target name")
	}
	if len(fake.Calls) != 0 {
		t.Errorf("Calls = %+v, want none", fake.Calls)
	}
}

func TestDock_NoCmdSession_ReturnsErrorWithNoEmbeddedPrefix(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "\n"}, // FindSessionID(cmd) -> no match
		},
	}
	d := &Deps{Tmux: tmux.Tmux{Runner: fake}, State: &state.State{}}

	err := Dock(d, "feature")
	if err == nil {
		t.Fatal("Dock returned nil error, want an error — the command center isn't running")
	}
	// Same "no embedded prefix" rule as TestCmd_ReservedNameCollision_ReturnsError.
	if strings.HasPrefix(err.Error(), "mt: ") || strings.HasPrefix(err.Error(), "mt cmd: ") {
		t.Errorf("Dock error = %q, want no embedded \"mt: \"/\"mt cmd: \" prefix (main.go adds its own)", err.Error())
	}
}

func dockTestState(name, repoPath string) *state.State {
	return &state.State{Sessions: []state.Session{
		{Name: name, Repos: []state.Repo{{RepoPath: repoPath, WorktreePath: "/wt"}}},
	}}
}

func TestDock_NoLiveSession_WritesMessageAndReturnsNil(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$9:__mt_cmd_center__\n"}, // FindSessionID(cmd)
			{Stdout: "\n"},                      // FindSessionID(feature) inside WindowTargetFor -> no match
		},
	}
	d := &Deps{Tmux: tmux.Tmux{Runner: fake}, State: dockTestState("feature", "/repos/myrepo")}

	if err := Dock(d, "feature"); err != nil {
		t.Fatalf("Dock returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"display-message", "-t", "$9:cmd.1", "mt cmd: 'feature' has no running session"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestDock_NoClaudePane_WritesMessageAndReturnsNil(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$9:__mt_cmd_center__\n"},          // FindSessionID(cmd)
			{Stdout: "$3:feature\n"},                    // FindSessionID(feature)
			{Stdout: "0 zsh\n1 nvim\n"},                  // FindClaudePane: no claude pane
		},
	}
	d := &Deps{Tmux: tmux.Tmux{Runner: fake}, State: dockTestState("feature", "/repos/myrepo")}

	if err := Dock(d, "feature"); err != nil {
		t.Fatalf("Dock returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-panes", "-t", "$3:myrepo", "-F", "#{pane_index} #{pane_current_command}"}},
		{Name: "tmux", Args: []string{"display-message", "-t", "$9:cmd.1", "mt cmd: no claude pane running for 'feature'"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestDock_AlreadyMirroring_OnlySelectsPane(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$9:__mt_cmd_center__\n"},       // FindSessionID(cmd)
			{Stdout: "$3:feature\n"},                 // FindSessionID(feature)
			{Stdout: "2 claude\n"},                   // FindClaudePane
			{Stdout: "/dev/ttys005\n"},                // DisplayMessage(#{pane_tty})
			{Stdout: "/dev/ttys005 $3\n"},             // ListClientSessions: already mirroring $3
			{},                                         // SelectPane(rightPane)
		},
	}
	d := &Deps{Tmux: tmux.Tmux{Runner: fake}, State: dockTestState("feature", "/repos/myrepo")}

	if err := Dock(d, "feature"); err != nil {
		t.Fatalf("Dock returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-panes", "-t", "$3:myrepo", "-F", "#{pane_index} #{pane_current_command}"}},
		{Name: "tmux", Args: []string{"display-message", "-p", "-t", "$9:cmd.1", "#{pane_tty}"}},
		{Name: "tmux", Args: []string{"list-clients", "-F", "#{client_tty} #{session_id}"}},
		{Name: "tmux", Args: []string{"select-pane", "-t", "$9:cmd.1"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v (no attach/switch-client — already mirroring)", fake.Calls, wantCalls)
	}
}

func TestDock_FirstDock_SendsAttachKeystroke(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$9:__mt_cmd_center__\n"}, // FindSessionID(cmd)
			{Stdout: "$3:feature\n"},           // FindSessionID(feature)
			{Stdout: "2 claude\n"},             // FindClaudePane
			{Stdout: "/dev/ttys005\n"},         // DisplayMessage(#{pane_tty})
			{Stdout: ""},                       // ListClientSessions: no clients yet
			{Stdout: "\n"},                     // SessionOption(@mt_cmd_docked) -> unset
			{},                                 // SelectWindow(target)
			{},                                 // SelectPane(target.2)
			{Stdout: "0\n"},                    // ZoomOn: display-message flag
			{},                                 // ZoomOn: resize-pane -Z
			{},                                 // SendKeys(attach keystroke)
			{},                                 // SetSessionOption(@mt_cmd_docked)
			{},                                 // SelectPane(rightPane)
		},
	}
	d := &Deps{Tmux: tmux.Tmux{Runner: fake}, State: dockTestState("feature", "/repos/myrepo")}

	if err := Dock(d, "feature"); err != nil {
		t.Fatalf("Dock returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-panes", "-t", "$3:myrepo", "-F", "#{pane_index} #{pane_current_command}"}},
		{Name: "tmux", Args: []string{"display-message", "-p", "-t", "$9:cmd.1", "#{pane_tty}"}},
		{Name: "tmux", Args: []string{"list-clients", "-F", "#{client_tty} #{session_id}"}},
		{Name: "tmux", Args: []string{"show-option", "-t", "$9", "-v", "@mt_cmd_docked"}},
		{Name: "tmux", Args: []string{"select-window", "-t", "$3:myrepo"}},
		{Name: "tmux", Args: []string{"select-pane", "-t", "$3:myrepo.2"}},
		{Name: "tmux", Args: []string{"display-message", "-p", "-t", "$3:myrepo", "#{window_zoomed_flag}"}},
		{Name: "tmux", Args: []string{"resize-pane", "-Z", "-t", "$3:myrepo"}},
		{Name: "tmux", Args: []string{"send-keys", "-t", "$9:cmd.1", "TMUX= tmux attach -t '$3'", "C-m"}},
		{Name: "tmux", Args: []string{"set-option", "-t", "$9", "@mt_cmd_docked", "feature"}},
		{Name: "tmux", Args: []string{"select-pane", "-t", "$9:cmd.1"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestDock_RedockDifferentSession_ZoomsOffPreviousFirst(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$9:__mt_cmd_center__\n"},  // FindSessionID(cmd)
			{Stdout: "$3:feature\n"},            // FindSessionID(feature)
			{Stdout: "2 claude\n"},              // FindClaudePane
			{Stdout: "/dev/ttys005\n"},          // DisplayMessage(#{pane_tty})
			{Stdout: ""},                        // ListClientSessions: no clients
			{Stdout: "other\n"},                 // SessionOption(@mt_cmd_docked) -> "other"
			{Stdout: "$4:other\n"},               // FindSessionID(other) inside WindowTargetFor for the un-dock
			{Stdout: "1\n"},                     // ZoomOff: display-message flag (zoomed)
			{},                                   // ZoomOff: resize-pane -Z
			{},                                   // SelectWindow(target)
			{},                                   // SelectPane(target.2)
			{Stdout: "0\n"},                      // ZoomOn: display-message flag
			{},                                   // ZoomOn: resize-pane -Z
			{},                                   // SendKeys(attach keystroke)
			{},                                   // SetSessionOption(@mt_cmd_docked)
			{},                                   // SelectPane(rightPane)
		},
	}
	st := dockTestState("feature", "/repos/myrepo")
	st.Sessions = append(st.Sessions, state.Session{Name: "other", Repos: []state.Repo{{RepoPath: "/repos/otherrepo", WorktreePath: "/wt2"}}})
	d := &Deps{Tmux: tmux.Tmux{Runner: fake}, State: st}

	if err := Dock(d, "feature"); err != nil {
		t.Fatalf("Dock returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-panes", "-t", "$3:myrepo", "-F", "#{pane_index} #{pane_current_command}"}},
		{Name: "tmux", Args: []string{"display-message", "-p", "-t", "$9:cmd.1", "#{pane_tty}"}},
		{Name: "tmux", Args: []string{"list-clients", "-F", "#{client_tty} #{session_id}"}},
		{Name: "tmux", Args: []string{"show-option", "-t", "$9", "-v", "@mt_cmd_docked"}},
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"display-message", "-p", "-t", "$4:otherrepo", "#{window_zoomed_flag}"}},
		{Name: "tmux", Args: []string{"resize-pane", "-Z", "-t", "$4:otherrepo"}},
		{Name: "tmux", Args: []string{"select-window", "-t", "$3:myrepo"}},
		{Name: "tmux", Args: []string{"select-pane", "-t", "$3:myrepo.2"}},
		{Name: "tmux", Args: []string{"display-message", "-p", "-t", "$3:myrepo", "#{window_zoomed_flag}"}},
		{Name: "tmux", Args: []string{"resize-pane", "-Z", "-t", "$3:myrepo"}},
		{Name: "tmux", Args: []string{"send-keys", "-t", "$9:cmd.1", "TMUX= tmux attach -t '$3'", "C-m"}},
		{Name: "tmux", Args: []string{"set-option", "-t", "$9", "@mt_cmd_docked", "feature"}},
		{Name: "tmux", Args: []string{"select-pane", "-t", "$9:cmd.1"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v (ZoomOff of previous window must precede SelectWindow of the new one)", fake.Calls, wantCalls)
	}
}

func TestDock_ExistingMirrorClient_UsesSwitchClientNotSendKeys(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$9:__mt_cmd_center__\n"}, // FindSessionID(cmd)
			{Stdout: "$3:feature\n"},           // FindSessionID(feature)
			{Stdout: "2 claude\n"},             // FindClaudePane
			{Stdout: "/dev/ttys005\n"},         // DisplayMessage(#{pane_tty})
			{Stdout: "/dev/ttys005 $4\n"},      // ListClientSessions: mirror exists, pointed at $4 (different session)
			{Stdout: "\n"},                     // SessionOption(@mt_cmd_docked) -> unset
			{},                                 // SelectWindow(target)
			{},                                 // SelectPane(target.2)
			{Stdout: "1\n"},                    // ZoomOn: display-message flag (already zoomed -> no toggle)
			{},                                 // SwitchClientTo
			{},                                 // SetSessionOption(@mt_cmd_docked)
			{},                                 // SelectPane(rightPane)
		},
	}
	d := &Deps{Tmux: tmux.Tmux{Runner: fake}, State: dockTestState("feature", "/repos/myrepo")}

	if err := Dock(d, "feature"); err != nil {
		t.Fatalf("Dock returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-panes", "-t", "$3:myrepo", "-F", "#{pane_index} #{pane_current_command}"}},
		{Name: "tmux", Args: []string{"display-message", "-p", "-t", "$9:cmd.1", "#{pane_tty}"}},
		{Name: "tmux", Args: []string{"list-clients", "-F", "#{client_tty} #{session_id}"}},
		{Name: "tmux", Args: []string{"show-option", "-t", "$9", "-v", "@mt_cmd_docked"}},
		{Name: "tmux", Args: []string{"select-window", "-t", "$3:myrepo"}},
		{Name: "tmux", Args: []string{"select-pane", "-t", "$3:myrepo.2"}},
		{Name: "tmux", Args: []string{"display-message", "-p", "-t", "$3:myrepo", "#{window_zoomed_flag}"}},
		{Name: "tmux", Args: []string{"switch-client", "-c", "/dev/ttys005", "-t", "$3"}},
		{Name: "tmux", Args: []string{"set-option", "-t", "$9", "@mt_cmd_docked", "feature"}},
		{Name: "tmux", Args: []string{"select-pane", "-t", "$9:cmd.1"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v (must use switch-client, not send-keys attach)", fake.Calls, wantCalls)
	}
}

func TestQuit_NoSession_Noop(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "\n"}, // FindSessionID(cmd) -> no match
		},
	}
	d := &Deps{Tmux: tmux.Tmux{Runner: fake}, State: &state.State{}}

	if err := Quit(d); err != nil {
		t.Fatalf("Quit returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestQuit_ZoomsOffDockedThenKillsSession(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$9:__mt_cmd_center__\n"}, // FindSessionID(cmd)
			{Stdout: "feature\n"},              // SessionOption(@mt_cmd_docked)
			{Stdout: "$3:feature\n"},           // FindSessionID(feature) inside WindowTargetFor
			{Stdout: "1\n"},                    // ZoomOff: display-message flag (zoomed)
			{},                                 // ZoomOff: resize-pane -Z
			{},                                 // KillSession
		},
	}
	d := &Deps{Tmux: tmux.Tmux{Runner: fake}, State: dockTestState("feature", "/repos/myrepo")}

	if err := Quit(d); err != nil {
		t.Fatalf("Quit returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"show-option", "-t", "$9", "-v", "@mt_cmd_docked"}},
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"display-message", "-p", "-t", "$3:myrepo", "#{window_zoomed_flag}"}},
		{Name: "tmux", Args: []string{"resize-pane", "-Z", "-t", "$3:myrepo"}},
		{Name: "tmux", Args: []string{"kill-session", "-t", "$9"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestCmdListFzfArgs_IncludesDockAndQuitBindings(t *testing.T) {
	got := cmdListFzfArgs("/usr/local/bin/mt", true, "/tmp/mt-cmd.sock")

	want := []string{
		`--delimiter=\t`,
		"--with-nth=2",
		"--height=100%",
		"--reverse",
		"--track",
		"--id-nth=1",
		"--listen=/tmp/mt-cmd.sock",
		"--bind=enter:execute-silent('/usr/local/bin/mt' __mt_cmd_dock {1})",
		"--bind=ctrl-c:execute-silent('/usr/local/bin/mt' __mt_cmd_quit)+abort",
		"--header=enter: show the selected claude pane on the right  ·  ctrl-c: quit command center",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cmdListFzfArgs() = %+v, want %+v", got, want)
	}
}

func TestCmdListFzfArgs_OmitsListenWhenUnsupported(t *testing.T) {
	got := cmdListFzfArgs("/usr/local/bin/mt", false, "/tmp/mt-cmd.sock")

	for _, arg := range got {
		if strings.HasPrefix(arg, "--listen=") {
			t.Errorf("cmdListFzfArgs() = %+v, want no --listen= entry when listen is false", got)
		}
	}
}

func TestQuit_NoDockedOption_StillKillsSession(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$9:__mt_cmd_center__\n"}, // FindSessionID(cmd)
			{Stdout: "\n"},                     // SessionOption(@mt_cmd_docked) -> unset
			{},                                 // KillSession
		},
	}
	d := &Deps{Tmux: tmux.Tmux{Runner: fake}, State: &state.State{}}

	if err := Quit(d); err != nil {
		t.Fatalf("Quit returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"show-option", "-t", "$9", "-v", "@mt_cmd_docked"}},
		{Name: "tmux", Args: []string{"kill-session", "-t", "$9"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestCmd_FirstRun_CreatesSessionSplitsAndSendsPicker(t *testing.T) {
	t.Setenv("HOME", "/home/user")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}

	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "\n"},    // FindSessionID(cmd) -> no match
			{Stdout: "$9\n"},  // NewSession
			{},                // SplitRightPane
			{},                // SendKeys
			{},                // SelectPane
			{},                // AttachOrSwitch
		},
	}
	d := &Deps{Tmux: tmux.Tmux{Runner: fake}, State: &state.State{}}

	if err := Cmd(d, "/usr/local/bin/mt", true); err != nil {
		t.Fatalf("Cmd returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{
			"new-session", "-d", "-s", CmdSessionName, "-n", "cmd", "-c", cwd,
			"-x", "200", "-y", "50", "-P", "-F", "#{session_id}",
		}},
		{Name: "tmux", Args: []string{"split-window", "-h", "-p", "70", "-t", "$9:cmd", "-c", "/home/user"}},
		{Name: "tmux", Args: []string{"send-keys", "-t", "$9:cmd.0", "'/usr/local/bin/mt' __mt_cmd_list", "C-m"}},
		{Name: "tmux", Args: []string{"select-pane", "-t", "$9:cmd.0"}},
		{Name: "tmux", Args: []string{"switch-client", "-t", "$9"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, wantCalls)
	}
}

func TestCmd_ExistingSession_DeadPicker_Relaunches(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$9:__mt_cmd_center__\n"}, // FindSessionID(cmd)
			{Stdout: "0 1234\n"},                // PanePID
			{Err: errors.New("exit status 1")},  // HasChildProcess -> dead
			{},                                  // SendKeys (relaunch)
			{},                                  // AttachOrSwitch
		},
	}
	d := &Deps{Tmux: tmux.Tmux{Runner: fake}, State: &state.State{}}

	if err := Cmd(d, "/usr/local/bin/mt", true); err != nil {
		t.Fatalf("Cmd returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-panes", "-t", "$9:cmd", "-F", "#{pane_index} #{pane_pid}"}},
		{Name: "pgrep", Args: []string{"-P", "1234"}},
		{Name: "tmux", Args: []string{"send-keys", "-t", "$9:cmd.0", "'/usr/local/bin/mt' __mt_cmd_list", "C-m"}},
		{Name: "tmux", Args: []string{"switch-client", "-t", "$9"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v (dead picker must relaunch)", fake.Calls, wantCalls)
	}
}

func TestCmd_ExistingSession_LivePicker_LeavesAlone(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$9:__mt_cmd_center__\n"}, // FindSessionID(cmd)
			{Stdout: "0 1234\n"},                // PanePID
			{Stdout: "5678\n"},                  // HasChildProcess -> alive
			{},                                  // AttachOrSwitch
		},
	}
	d := &Deps{Tmux: tmux.Tmux{Runner: fake}, State: &state.State{}}

	if err := Cmd(d, "/usr/local/bin/mt", true); err != nil {
		t.Fatalf("Cmd returned error: %v", err)
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
		{Name: "tmux", Args: []string{"list-panes", "-t", "$9:cmd", "-F", "#{pane_index} #{pane_pid}"}},
		{Name: "pgrep", Args: []string{"-P", "1234"}},
		{Name: "tmux", Args: []string{"switch-client", "-t", "$9"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v (live picker must not be relaunched)", fake.Calls, wantCalls)
	}
}

// TestCmd_ReservedNameCollision_ReturnsError is not in the plan's named
// test list for this task, but ports a real bash do_cmd branch (the
// reserved-session-name collision guard) that every other Cmd test above
// bypasses by using an empty State — without it, that guard would ship
// unverified. See the ledger for this addition.
func TestCmd_ReservedNameCollision_ReturnsError(t *testing.T) {
	fake := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "$9:__mt_cmd_center__\n"}, // FindSessionID(cmd)
		},
	}
	d := &Deps{
		Tmux: tmux.Tmux{Runner: fake},
		State: &state.State{Sessions: []state.Session{
			{Name: CmdSessionName, Repos: []state.Repo{{RepoPath: "/repos/myrepo", WorktreePath: "/wt"}}},
		}},
	}

	err := Cmd(d, "/usr/local/bin/mt", true)
	if err == nil {
		t.Fatal("Cmd returned nil error, want an error about the reserved name collision")
	}
	// Must not itself start with "mt: " or "mt cmd: " — main.go prepends
	// "mt: " to every returned error, so an error string that embeds its
	// own prefix doubles up on the real terminal (e.g. "mt: mt cmd: ...").
	if strings.HasPrefix(err.Error(), "mt: ") || strings.HasPrefix(err.Error(), "mt cmd: ") {
		t.Errorf("Cmd error = %q, want no embedded \"mt: \"/\"mt cmd: \" prefix (main.go adds its own)", err.Error())
	}

	wantCalls := []executil.Call{
		{Name: "tmux", Args: []string{"list-sessions", "-F", "#{session_id}:#{session_name}"}},
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v (must not touch tmux further once the collision is detected)", fake.Calls, wantCalls)
	}
}
