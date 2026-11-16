package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mauricebutts/motree/internal/statusfile"
)

func TestRun_WritesStatusForKnownEvent(t *testing.T) {
	cwd := t.TempDir()
	defer cleanupStatus(cwd)

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
	cwd := t.TempDir()
	defer cleanupStatus(cwd)

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

func TestRun_UnknownEvent_NoopNoError(t *testing.T) {
	cwd := t.TempDir()
	defer cleanupStatus(cwd)

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
	cwd := t.TempDir()
	defer cleanupStatus(cwd)

	input := `{this is not valid json`
	r := bytes.NewReader([]byte(input))

	err := Run(r)
	if err != nil {
		t.Fatalf("Run returned error for malformed JSON: %v", err)
	}

	// Verify no status file was created
	_, found, err := statusfile.Read(cwd)
	if err != nil {
		t.Fatalf("Read status failed: %v", err)
	}
	if found {
		t.Fatal("status file should not be created for malformed JSON")
	}
}

func TestRun_UntrackedCwd_StillWrites(t *testing.T) {
	cwd := "/tmp/nonexistent/deeply/nested/path"
	defer cleanupStatus(cwd)

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

// cleanupStatus removes status files created during tests
func cleanupStatus(cwd string) {
	statusDir := statusfile.Dir()
	statusPath := filepath.Join(statusDir, statusfile.EscapeCwd(cwd))
	_ = os.Remove(statusPath)
}
