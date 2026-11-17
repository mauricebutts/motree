package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mauricebutts/motree/internal/statusfile"
)

// setupTempStateFile sets up a temporary state file directory and env var.
// Returns a cleanup function that restores the original env var.
func setupTempStateFile(t *testing.T) func() {
	// Save original env var
	oldEnv, exists := os.LookupEnv("MOTREE_STATE_FILE")

	// Use a temporary directory for the state file
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, ".motree")
	os.Setenv("MOTREE_STATE_FILE", stateFilePath)

	// Return cleanup function
	return func() {
		if exists {
			os.Setenv("MOTREE_STATE_FILE", oldEnv)
		} else {
			os.Unsetenv("MOTREE_STATE_FILE")
		}
	}
}

func TestRun_WritesStatusForKnownEvent(t *testing.T) {
	cleanup := setupTempStateFile(t)
	defer cleanup()

	cwd := "/home/user/project"
	input := `{"hook_event_name": "PreToolUse", "cwd": "` + cwd + `"}`
	r := bytes.NewReader([]byte(input))

	err := Run(r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Verify status was written
	status, found, err := statusfile.Read(cwd)
	if err != nil {
		t.Fatalf("Read status failed: %v", err)
	}
	if !found {
		t.Fatal("status file not found after Run")
	}
	if status != statusfile.Running {
		t.Errorf("expected status Running, got %s", status)
	}
}

func TestRun_ClearsStatusOnSessionEnd(t *testing.T) {
	cleanup := setupTempStateFile(t)
	defer cleanup()

	cwd := "/home/user/project"

	// Write initial status
	if err := statusfile.Write(cwd, statusfile.Running); err != nil {
		t.Fatalf("failed to write initial status: %v", err)
	}

	// Verify it exists
	_, found, err := statusfile.Read(cwd)
	if err != nil || !found {
		t.Fatal("failed to verify initial status was written")
	}

	// Call Run with SessionEnd event
	input := `{"hook_event_name": "SessionEnd", "cwd": "` + cwd + `"}`
	r := bytes.NewReader([]byte(input))

	err = Run(r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Verify status was cleared
	_, found, err = statusfile.Read(cwd)
	if err != nil {
		t.Fatalf("Read status failed: %v", err)
	}
	if found {
		t.Fatal("status file still exists after SessionEnd")
	}
}

func TestRun_NotificationIdlePrompt_WritesStopped(t *testing.T) {
	cleanup := setupTempStateFile(t)
	defer cleanup()

	cwd := "/home/user/project"
	input := `{"hook_event_name": "Notification", "notification_type": "idle_prompt", "cwd": "` + cwd + `"}`
	r := bytes.NewReader([]byte(input))

	err := Run(r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	status, found, err := statusfile.Read(cwd)
	if err != nil {
		t.Fatalf("Read status failed: %v", err)
	}
	if !found {
		t.Fatal("status file not found after Run")
	}
	if status != statusfile.Stopped {
		t.Errorf("expected status Stopped, got %s", status)
	}
}

func TestRun_NotificationPermissionPrompt_WritesWaiting(t *testing.T) {
	cleanup := setupTempStateFile(t)
	defer cleanup()

	cwd := "/home/user/project"
	input := `{"hook_event_name": "Notification", "notification_type": "permission_prompt", "cwd": "` + cwd + `"}`
	r := bytes.NewReader([]byte(input))

	err := Run(r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	status, found, err := statusfile.Read(cwd)
	if err != nil {
		t.Fatalf("Read status failed: %v", err)
	}
	if !found {
		t.Fatal("status file not found after Run")
	}
	if status != statusfile.Waiting {
		t.Errorf("expected status Waiting, got %s", status)
	}
}

func TestRun_NotificationUnrecognizedType_NoopNoError(t *testing.T) {
	cleanup := setupTempStateFile(t)
	defer cleanup()

	cwd := "/home/user/project"
	input := `{"hook_event_name": "Notification", "notification_type": "auth_success", "cwd": "` + cwd + `"}`
	r := bytes.NewReader([]byte(input))

	err := Run(r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	_, found, err := statusfile.Read(cwd)
	if err != nil {
		t.Fatalf("Read status failed: %v", err)
	}
	if found {
		t.Fatal("status file should not be created for an unrecognized notification_type")
	}
}

func TestRun_SubagentPreToolUse_NoopNoError(t *testing.T) {
	cleanup := setupTempStateFile(t)
	defer cleanup()

	cwd := "/home/user/project"
	input := `{"hook_event_name": "PreToolUse", "cwd": "` + cwd + `", "agent_id": "agent-123", "agent_type": "general-purpose"}`
	r := bytes.NewReader([]byte(input))

	err := Run(r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	_, found, err := statusfile.Read(cwd)
	if err != nil {
		t.Fatalf("Read status failed: %v", err)
	}
	if found {
		t.Fatal("status file should not be created for a subagent-originated event")
	}
}

func TestRun_SubagentStop_NoopNoError(t *testing.T) {
	cleanup := setupTempStateFile(t)
	defer cleanup()

	cwd := "/home/user/project"

	// The parent session is Running when the subagent finishes.
	if err := statusfile.Write(cwd, statusfile.Running); err != nil {
		t.Fatalf("failed to write initial status: %v", err)
	}

	input := `{"hook_event_name": "SubagentStop", "cwd": "` + cwd + `", "agent_id": "agent-123", "agent_type": "general-purpose"}`
	r := bytes.NewReader([]byte(input))

	err := Run(r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	status, found, err := statusfile.Read(cwd)
	if err != nil {
		t.Fatalf("Read status failed: %v", err)
	}
	if !found {
		t.Fatal("status file should still exist")
	}
	if status != statusfile.Running {
		t.Errorf("SubagentStop should not overwrite the parent's status, got %s", status)
	}
}

func TestRun_UnknownEvent_NoopNoError(t *testing.T) {
	cleanup := setupTempStateFile(t)
	defer cleanup()

	cwd := "/home/user/project"
	input := `{"hook_event_name": "UnknownEvent", "cwd": "` + cwd + `"}`
	r := bytes.NewReader([]byte(input))

	err := Run(r)
	if err != nil {
		t.Fatalf("Run returned error for unknown event: %v", err)
	}

	// Verify no status file was created
	_, found, err := statusfile.Read(cwd)
	if err != nil {
		t.Fatalf("Read status failed: %v", err)
	}
	if found {
		t.Fatal("status file should not be created for unknown event")
	}
}

func TestRun_MalformedJSON_NoopNoError(t *testing.T) {
	cleanup := setupTempStateFile(t)
	defer cleanup()

	input := `{this is not valid json`
	r := bytes.NewReader([]byte(input))

	err := Run(r)
	if err != nil {
		t.Fatalf("Run returned error for malformed JSON: %v", err)
	}

	// Verify no status file was created (no error, but also no write attempted)
	cwd := "/some/test/path"
	_, found, err := statusfile.Read(cwd)
	if err != nil {
		t.Fatalf("Read status failed: %v", err)
	}
	if found {
		t.Fatal("status file should not be created for malformed JSON")
	}
}

func TestRun_UntrackedCwd_StillWrites(t *testing.T) {
	cleanup := setupTempStateFile(t)
	defer cleanup()

	cwd := "/tmp/nonexistent/deeply/nested/path"
	input := `{"hook_event_name": "UserPromptSubmit", "cwd": "` + cwd + `"}`
	r := bytes.NewReader([]byte(input))

	err := Run(r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Verify status was written even for nonexistent cwd
	status, found, err := statusfile.Read(cwd)
	if err != nil {
		t.Fatalf("Read status failed: %v", err)
	}
	if !found {
		t.Fatal("status file not found after Run for nonexistent cwd")
	}
	if status != statusfile.Running {
		t.Errorf("expected status Running, got %s", status)
	}
}

func TestRun_PropagatesStatusfileWriteError(t *testing.T) {
	cleanup := setupTempStateFile(t)
	defer cleanup()

	// Make the status directory read-only to cause Write to fail
	statusDir := statusfile.Dir()
	if err := os.MkdirAll(statusDir, 0755); err != nil {
		t.Fatalf("failed to create status dir: %v", err)
	}

	// Save original permissions
	fileInfo, err := os.Stat(statusDir)
	if err != nil {
		t.Fatalf("failed to stat status dir: %v", err)
	}
	originalMode := fileInfo.Mode()

	// Make read-only to prevent writes
	if err := os.Chmod(statusDir, 0555); err != nil {
		t.Fatalf("failed to chmod status dir: %v", err)
	}
	defer os.Chmod(statusDir, originalMode)

	cwd := "/home/user/project"
	input := `{"hook_event_name": "PreToolUse", "cwd": "` + cwd + `"}`
	r := bytes.NewReader([]byte(input))

	// Run should return an error since it can't write to read-only dir
	err = Run(r)
	if err == nil {
		t.Fatal("Run should return error when statusfile.Write fails")
	}
}
