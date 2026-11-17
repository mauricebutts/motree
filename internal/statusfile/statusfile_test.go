package statusfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEscapeCwd_ReplacesDotsSlashesUnderscores(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{".", "-"},
		{"/", "-"},
		{"_", "-"},
		{"/home/user/project", "-home-user-project"},
		{"./path/to/dir", "--path-to-dir"},
		{"path_with_underscores", "path-with-underscores"},
		{"normal-path/to.dir_name", "normal-path-to-dir-name"},
		{"/var/log", "-var-log"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := EscapeCwd(tt.input)
			if result != tt.expected {
				t.Errorf("EscapeCwd(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestEventStatus_MapsAllTenEvents(t *testing.T) {
	tests := []struct {
		event  string
		status Status
		clear  bool
		ok     bool
	}{
		{"SessionStart", Stopped, false, true},
		{"UserPromptSubmit", Running, false, true},
		{"PreToolUse", Running, false, true},
		{"PostToolUse", Running, false, true},
		{"Notification", Waiting, false, true},
		{"PermissionRequest", Waiting, false, true},
		{"SubagentStop", Subagents, false, true},
		{"Stop", Stopped, false, true},
		{"StopFailure", Stopped, false, true},
		{"SessionEnd", "", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			status, clear, ok := EventStatus(tt.event)
			if status != tt.status || clear != tt.clear || ok != tt.ok {
				t.Errorf("EventStatus(%q) = (%q, %v, %v), want (%q, %v, %v)",
					tt.event, status, clear, ok, tt.status, tt.clear, tt.ok)
			}
		})
	}
}

func TestEventStatus_UnknownEvent_NotOk(t *testing.T) {
	status, clear, ok := EventStatus("UnknownEvent")
	if ok {
		t.Errorf("EventStatus(\"UnknownEvent\") ok = true, want false")
	}
	if status != "" {
		t.Errorf("EventStatus(\"UnknownEvent\") status = %q, want empty", status)
	}
	if clear {
		t.Errorf("EventStatus(\"UnknownEvent\") clear = true, want false")
	}
}

func TestWriteReadClear_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	statusDir := filepath.Join(tmpDir, "status")
	os.MkdirAll(statusDir, 0755)

	cwd := "/home/user/project"
	status := Running

	// Write a status file
	err := writeStatus(statusDir, cwd, status)
	if err != nil {
		t.Fatalf("writeStatus failed: %v", err)
	}

	// Read it back
	readStat, found, err := readStatus(statusDir, cwd)
	if err != nil {
		t.Fatalf("readStatus failed: %v", err)
	}
	if !found {
		t.Errorf("readStatus returned found=false, want true")
	}
	if readStat != status {
		t.Errorf("readStatus returned status=%q, want %q", readStat, status)
	}

	// Clear it
	err = clearStatus(statusDir, cwd)
	if err != nil {
		t.Fatalf("clearStatus failed: %v", err)
	}

	// Verify it's gone
	readStat, found, err = readStatus(statusDir, cwd)
	if err != nil {
		t.Fatalf("readStatus after clearStatus failed: %v", err)
	}
	if found {
		t.Errorf("readStatus after clearStatus returned found=true, want false")
	}
}

func TestWrite_ForUntrackedCwd_StillSucceeds(t *testing.T) {
	tmpDir := t.TempDir()
	statusDir := filepath.Join(tmpDir, "status")
	os.MkdirAll(statusDir, 0755)

	// Use an untracked cwd (doesn't exist in any filesystem)
	cwd := "/some/deeply/nested/untracked/path/that/doesnt/exist"
	status := Waiting

	// Write should still succeed
	err := writeStatus(statusDir, cwd, status)
	if err != nil {
		t.Fatalf("writeStatus for untracked cwd failed: %v", err)
	}

	// Verify we can read it back
	readStat, found, err := readStatus(statusDir, cwd)
	if err != nil {
		t.Fatalf("readStatus failed: %v", err)
	}
	if !found {
		t.Errorf("readStatus returned found=false, want true")
	}
	if readStat != status {
		t.Errorf("readStatus returned status=%q, want %q", readStat, status)
	}
}

func TestPublicWrite_RoundTrip(t *testing.T) {
	// Save original env var
	oldEnv, exists := os.LookupEnv("MOTREE_STATE_FILE")
	defer func() {
		if exists {
			os.Setenv("MOTREE_STATE_FILE", oldEnv)
		} else {
			os.Unsetenv("MOTREE_STATE_FILE")
		}
	}()

	// Use a temporary directory for the state file
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, ".motree")
	os.Setenv("MOTREE_STATE_FILE", stateFilePath)

	cwd := "/home/user/project"
	status := Running

	// Write using public API
	err := Write(cwd, status)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Read using public API
	readStat, found, err := Read(cwd)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if !found {
		t.Errorf("Read returned found=false, want true")
	}
	if readStat != status {
		t.Errorf("Read returned status=%q, want %q", readStat, status)
	}

	// Clear using public API
	err = Clear(cwd)
	if err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// Verify it's gone
	readStat, found, err = Read(cwd)
	if err != nil {
		t.Fatalf("Read after Clear failed: %v", err)
	}
	if found {
		t.Errorf("Read after Clear returned found=true, want false")
	}
}

func TestPublicWrite_ForUntrackedCwd_StillSucceeds(t *testing.T) {
	// Save original env var
	oldEnv, exists := os.LookupEnv("MOTREE_STATE_FILE")
	defer func() {
		if exists {
			os.Setenv("MOTREE_STATE_FILE", oldEnv)
		} else {
			os.Unsetenv("MOTREE_STATE_FILE")
		}
	}()

	// Use a temporary directory for the state file
	tmpDir := t.TempDir()
	stateFilePath := filepath.Join(tmpDir, ".motree")
	os.Setenv("MOTREE_STATE_FILE", stateFilePath)

	// Use an untracked cwd (doesn't exist in any filesystem)
	cwd := "/some/deeply/nested/untracked/path/that/doesnt/exist"
	status := Waiting

	// Write using public API should still succeed
	err := Write(cwd, status)
	if err != nil {
		t.Fatalf("Write for untracked cwd failed: %v", err)
	}

	// Verify we can read it back using public API
	readStat, found, err := Read(cwd)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if !found {
		t.Errorf("Read returned found=false, want true")
	}
	if readStat != status {
		t.Errorf("Read returned status=%q, want %q", readStat, status)
	}
}

func TestDir_ReturnsStateFilePathPlusSuffix(t *testing.T) {
	// Save original env var
	oldEnv, exists := os.LookupEnv("MOTREE_STATE_FILE")
	defer func() {
		if exists {
			os.Setenv("MOTREE_STATE_FILE", oldEnv)
		} else {
			os.Unsetenv("MOTREE_STATE_FILE")
		}
	}()

	expectedPath := "/tmp/test_state"
	os.Setenv("MOTREE_STATE_FILE", expectedPath)

	got := Dir()
	want := expectedPath + "-status"

	if got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestPathFor_CombinesDirAndEscapedCwd(t *testing.T) {
	// Save original env var
	oldEnv, exists := os.LookupEnv("MOTREE_STATE_FILE")
	defer func() {
		if exists {
			os.Setenv("MOTREE_STATE_FILE", oldEnv)
		} else {
			os.Unsetenv("MOTREE_STATE_FILE")
		}
	}()

	os.Setenv("MOTREE_STATE_FILE", "/tmp/test_state")

	cwd := "/home/user/project"
	got := PathFor(cwd)
	want := filepath.Join("/tmp/test_state-status", "-home-user-project")

	if got != want {
		t.Errorf("PathFor(%q) = %q, want %q", cwd, got, want)
	}
}
