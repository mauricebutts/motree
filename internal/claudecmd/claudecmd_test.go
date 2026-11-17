package claudecmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResumeCommand_NoProjectDir_ReturnsClaude(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	result := ResumeCommand("/some/working/directory")
	if result != "claude" {
		t.Errorf("expected 'claude', got %q", result)
	}
}

func TestResumeCommand_EmptyProjectDir_ReturnsClaude(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	projectDir := filepath.Join(tempHome, ".claude", "projects", "-some-working-directory")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("failed to create project directory: %v", err)
	}

	result := ResumeCommand("/some/working/directory")
	if result != "claude" {
		t.Errorf("expected 'claude', got %q", result)
	}
}

func TestResumeCommand_NonEmptyProjectDir_ReturnsClaudeDashC(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	projectDir := filepath.Join(tempHome, ".claude", "projects", "-some-working-directory")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("failed to create project directory: %v", err)
	}

	// Create a file in the project directory to make it non-empty
	testFile := filepath.Join(projectDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	result := ResumeCommand("/some/working/directory")
	if result != "claude -c" {
		t.Errorf("expected 'claude -c', got %q", result)
	}
}
