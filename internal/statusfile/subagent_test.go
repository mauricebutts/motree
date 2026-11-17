package statusfile

import (
	"os"
	"testing"
)

// setSubagentHome points MOTREE_STATE_FILE at a fresh temp file, so every
// subagent-scoped function in this file operates against an isolated,
// empty directory tree for the duration of the test.
func setSubagentHome(t *testing.T) {
	t.Helper()
	t.Setenv("MOTREE_STATE_FILE", t.TempDir()+"/.motree")
}

func TestWriteReadClearSubagent_RoundTrip(t *testing.T) {
	setSubagentHome(t)

	cwd := "/home/user/project"
	agentID := "agent-123"

	if err := WriteSubagent(cwd, agentID, Running); err != nil {
		t.Fatalf("WriteSubagent: %v", err)
	}

	status, found, err := ReadSubagent(cwd, agentID)
	if err != nil {
		t.Fatalf("ReadSubagent: %v", err)
	}
	if !found || status != Running {
		t.Errorf("ReadSubagent = (%q, %v), want (%q, true)", status, found, Running)
	}

	if err := ClearSubagent(cwd, agentID); err != nil {
		t.Fatalf("ClearSubagent: %v", err)
	}

	status, found, err = ReadSubagent(cwd, agentID)
	if err != nil {
		t.Fatalf("ReadSubagent after Clear: %v", err)
	}
	if found {
		t.Errorf("ReadSubagent after Clear = (%q, true), want found=false", status)
	}
}

func TestReadSubagent_NeverWritten_NotFound(t *testing.T) {
	setSubagentHome(t)

	status, found, err := ReadSubagent("/nope", "agent-x")
	if err != nil {
		t.Fatalf("ReadSubagent: %v", err)
	}
	if found || status != "" {
		t.Errorf("ReadSubagent = (%q, %v), want (\"\", false)", status, found)
	}
}

func TestListSubagents_OnlyMatchesGivenCwd(t *testing.T) {
	setSubagentHome(t)

	if err := WriteSubagent("/project/a", "agent-1", Running); err != nil {
		t.Fatalf("WriteSubagent: %v", err)
	}
	if err := WriteSubagent("/project/a", "agent-2", Waiting); err != nil {
		t.Fatalf("WriteSubagent: %v", err)
	}
	if err := WriteSubagent("/project/b", "agent-3", Stopped); err != nil {
		t.Fatalf("WriteSubagent: %v", err)
	}

	subs, err := ListSubagents("/project/a")
	if err != nil {
		t.Fatalf("ListSubagents: %v", err)
	}
	if len(subs) != 2 {
		t.Fatalf("ListSubagents(/project/a) returned %d entries, want 2: %+v", len(subs), subs)
	}

	var statuses []Status
	for _, s := range subs {
		statuses = append(statuses, s.Status)
	}
	wantRunning, wantWaiting := false, false
	for _, s := range statuses {
		if s == Running {
			wantRunning = true
		}
		if s == Waiting {
			wantWaiting = true
		}
	}
	if !wantRunning || !wantWaiting {
		t.Errorf("ListSubagents(/project/a) statuses = %v, want one Running and one Waiting", statuses)
	}
}

func TestListSubagents_NoneTracked_ReturnsNilNoError(t *testing.T) {
	setSubagentHome(t)

	subs, err := ListSubagents("/nowhere")
	if err != nil {
		t.Fatalf("ListSubagents: %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("ListSubagents(/nowhere) = %+v, want none", subs)
	}
}

func TestSubagentInflight_MarkThenClear_RoundTrip(t *testing.T) {
	setSubagentHome(t)

	cwd := "/home/user/project"
	agentID := "agent-123"

	has, err := HasSubagentInflight(cwd, agentID)
	if err != nil {
		t.Fatalf("HasSubagentInflight: %v", err)
	}
	if has {
		t.Fatalf("HasSubagentInflight before any mark = true, want false")
	}

	if err := MarkSubagentInflight(cwd, agentID, "toolu_1"); err != nil {
		t.Fatalf("MarkSubagentInflight: %v", err)
	}

	has, err = HasSubagentInflight(cwd, agentID)
	if err != nil {
		t.Fatalf("HasSubagentInflight: %v", err)
	}
	if !has {
		t.Errorf("HasSubagentInflight after mark = false, want true")
	}

	if err := ClearSubagentInflight(cwd, agentID, "toolu_1"); err != nil {
		t.Fatalf("ClearSubagentInflight: %v", err)
	}

	has, err = HasSubagentInflight(cwd, agentID)
	if err != nil {
		t.Fatalf("HasSubagentInflight: %v", err)
	}
	if has {
		t.Errorf("HasSubagentInflight after clear = true, want false")
	}
}

// TestSubagentInflight_ConcurrentCalls_BothMustClearBeforeFalse pins the
// reason inflight tracking is keyed by tool_use_id rather than just
// agent_id: an actor dispatching two subagents in one turn issues two
// Agent-tool calls back to back, and the first one returning must not make
// the actor look done with "its own subagent" while the second is still
// running.
func TestSubagentInflight_ConcurrentCalls_BothMustClearBeforeFalse(t *testing.T) {
	setSubagentHome(t)

	cwd := "/home/user/project"
	agentID := "agent-123"

	if err := MarkSubagentInflight(cwd, agentID, "toolu_1"); err != nil {
		t.Fatalf("MarkSubagentInflight(toolu_1): %v", err)
	}
	if err := MarkSubagentInflight(cwd, agentID, "toolu_2"); err != nil {
		t.Fatalf("MarkSubagentInflight(toolu_2): %v", err)
	}

	if err := ClearSubagentInflight(cwd, agentID, "toolu_1"); err != nil {
		t.Fatalf("ClearSubagentInflight(toolu_1): %v", err)
	}

	has, err := HasSubagentInflight(cwd, agentID)
	if err != nil {
		t.Fatalf("HasSubagentInflight: %v", err)
	}
	if !has {
		t.Errorf("HasSubagentInflight after clearing only one of two = false, want true")
	}

	if err := ClearSubagentInflight(cwd, agentID, "toolu_2"); err != nil {
		t.Fatalf("ClearSubagentInflight(toolu_2): %v", err)
	}

	has, err = HasSubagentInflight(cwd, agentID)
	if err != nil {
		t.Fatalf("HasSubagentInflight: %v", err)
	}
	if has {
		t.Errorf("HasSubagentInflight after clearing both = true, want false")
	}
}

func TestReadSubagent_InflightOverridesWrittenStatus(t *testing.T) {
	setSubagentHome(t)

	cwd := "/home/user/project"
	agentID := "agent-123"

	if err := WriteSubagent(cwd, agentID, Running); err != nil {
		t.Fatalf("WriteSubagent: %v", err)
	}
	if err := MarkSubagentInflight(cwd, agentID, "toolu_1"); err != nil {
		t.Fatalf("MarkSubagentInflight: %v", err)
	}

	status, found, err := ReadSubagent(cwd, agentID)
	if err != nil {
		t.Fatalf("ReadSubagent: %v", err)
	}
	if !found || status != Subagents {
		t.Errorf("ReadSubagent with an inflight Agent-tool call = (%q, %v), want (%q, true)", status, found, Subagents)
	}
}

func TestListSubagents_InflightOverridesWrittenStatus(t *testing.T) {
	setSubagentHome(t)

	cwd := "/home/user/project"
	agentID := "agent-123"

	if err := WriteSubagent(cwd, agentID, Waiting); err != nil {
		t.Fatalf("WriteSubagent: %v", err)
	}
	if err := MarkSubagentInflight(cwd, agentID, "toolu_1"); err != nil {
		t.Fatalf("MarkSubagentInflight: %v", err)
	}

	subs, err := ListSubagents(cwd)
	if err != nil {
		t.Fatalf("ListSubagents: %v", err)
	}
	if len(subs) != 1 || subs[0].Status != Subagents {
		t.Errorf("ListSubagents = %+v, want a single Subagents entry", subs)
	}
}

func TestClearSubagent_AlsoClearsItsInflightMarkers(t *testing.T) {
	setSubagentHome(t)

	cwd := "/home/user/project"
	agentID := "agent-123"

	if err := WriteSubagent(cwd, agentID, Running); err != nil {
		t.Fatalf("WriteSubagent: %v", err)
	}
	if err := MarkSubagentInflight(cwd, agentID, "toolu_1"); err != nil {
		t.Fatalf("MarkSubagentInflight: %v", err)
	}

	if err := ClearSubagent(cwd, agentID); err != nil {
		t.Fatalf("ClearSubagent: %v", err)
	}

	has, err := HasSubagentInflight(cwd, agentID)
	if err != nil {
		t.Fatalf("HasSubagentInflight: %v", err)
	}
	if has {
		t.Errorf("HasSubagentInflight after ClearSubagent = true, want false — stale marker left behind")
	}
}

func TestSubagentInflight_DistinctAgentsUnderSameCwd_DontCollide(t *testing.T) {
	setSubagentHome(t)

	cwd := "/home/user/project"

	if err := MarkSubagentInflight(cwd, "agent-1", "toolu_1"); err != nil {
		t.Fatalf("MarkSubagentInflight: %v", err)
	}

	has, err := HasSubagentInflight(cwd, "agent-2")
	if err != nil {
		t.Fatalf("HasSubagentInflight: %v", err)
	}
	if has {
		t.Errorf("HasSubagentInflight(agent-2) = true, want false — agent-1's marker must not leak onto agent-2")
	}

	has, err = HasSubagentInflight(cwd, "agent-1")
	if err != nil {
		t.Fatalf("HasSubagentInflight: %v", err)
	}
	if !has {
		t.Errorf("HasSubagentInflight(agent-1) = false, want true")
	}
}

func TestSubagentEventStatus_MapsEveryKnownEvent(t *testing.T) {
	tests := []struct {
		event  string
		status Status
		clear  bool
		ok     bool
	}{
		{"SubagentStart", Running, false, true},
		{"PreToolUse", Running, false, true},
		{"PostToolUse", Running, false, true},
		{"PermissionRequest", Waiting, false, true},
		{"SubagentStop", "", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			status, clear, ok := SubagentEventStatus(tt.event, "")
			if status != tt.status || clear != tt.clear || ok != tt.ok {
				t.Errorf("SubagentEventStatus(%q) = (%q, %v, %v), want (%q, %v, %v)",
					tt.event, status, clear, ok, tt.status, tt.clear, tt.ok)
			}
		})
	}
}

func TestSubagentEventStatus_Notification_MapsByNotificationType(t *testing.T) {
	status, clear, ok := SubagentEventStatus("Notification", "permission_prompt")
	if status != Waiting || clear || !ok {
		t.Errorf("SubagentEventStatus(Notification, permission_prompt) = (%q, %v, %v), want (%q, false, true)", status, clear, ok, Waiting)
	}

	status, clear, ok = SubagentEventStatus("Notification", "some_unrecognized_type")
	if ok {
		t.Errorf("SubagentEventStatus(Notification, unrecognized) ok = true, want false (got status=%q clear=%v)", status, clear)
	}
}

func TestSubagentEventStatus_UnknownEvent_NotOk(t *testing.T) {
	status, clear, ok := SubagentEventStatus("UnknownEvent", "")
	if ok || status != "" || clear {
		t.Errorf("SubagentEventStatus(UnknownEvent) = (%q, %v, %v), want (\"\", false, false)", status, clear, ok)
	}
}

func TestSubagentDir_ReturnsStateFilePathPlusSuffix(t *testing.T) {
	expectedPath := "/tmp/test_state"
	os.Setenv("MOTREE_STATE_FILE", expectedPath)
	t.Cleanup(func() { os.Unsetenv("MOTREE_STATE_FILE") })

	got := SubagentDir()
	want := expectedPath + "-status-subagents"

	if got != want {
		t.Errorf("SubagentDir() = %q, want %q", got, want)
	}
}
