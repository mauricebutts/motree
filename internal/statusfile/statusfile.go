package statusfile

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mauricebutts/motree/internal/state"
)

// Status represents the current status of a Claude Code session
type Status string

const (
	Running   Status = "running"
	Waiting   Status = "waiting"
	Subagents Status = "subagents"
	Stopped   Status = "stopped"
)

// EscapeCwd replaces '.', '/', '_' with '-' for safe filesystem names
func EscapeCwd(cwd string) string {
	return strings.NewReplacer(".", "-", "/", "-", "_", "-").Replace(cwd)
}

// Dir returns the status directory path
func Dir() string {
	return state.StateFilePath() + "-status"
}

// PathFor returns the full status file path for a given cwd
func PathFor(cwd string) string {
	return filepath.Join(Dir(), EscapeCwd(cwd))
}

// EventStatus maps a Claude Code hook event to a status, clear flag, and ok flag
func EventStatus(event string) (status Status, clear bool, ok bool) {
	switch event {
	case "SessionStart":
		return Stopped, false, true
	case "UserPromptSubmit":
		return Running, false, true
	case "PreToolUse":
		return Running, false, true
	case "PostToolUse":
		return Running, false, true
	case "Notification":
		return Waiting, false, true
	case "PermissionRequest":
		return Waiting, false, true
	case "SubagentStop":
		return Subagents, false, true
	case "Stop":
		return Stopped, false, true
	case "StopFailure":
		return Stopped, false, true
	case "SessionEnd":
		return "", true, true
	default:
		return "", false, false
	}
}

// Write atomically writes a status file for the given cwd
func Write(cwd string, status Status) error {
	return writeStatus(Dir(), cwd, status)
}

// Read reads the status file for the given cwd
func Read(cwd string) (status Status, found bool, err error) {
	return readStatus(Dir(), cwd)
}

// Clear removes the status file for the given cwd
func Clear(cwd string) error {
	return clearStatus(Dir(), cwd)
}

// Internal functions that accept directory path for testability

func writeStatus(statusDir string, cwd string, status Status) error {
	// Ensure status directory exists
	if err := os.MkdirAll(statusDir, 0755); err != nil {
		return fmt.Errorf("failed to create status directory: %w", err)
	}

	path := filepath.Join(statusDir, EscapeCwd(cwd))
	content := string(status)

	// Write to temp file first with unique suffix for atomic write
	tmpPath := path + ".tmp." + fmt.Sprintf("%d%d", time.Now().UnixNano(), rand.Intn(1000000))
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write temp status file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		// Best effort cleanup
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp status file: %w", err)
	}

	return nil
}

func readStatus(statusDir string, cwd string) (status Status, found bool, err error) {
	path := filepath.Join(statusDir, EscapeCwd(cwd))

	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to read status file: %w", err)
	}

	return Status(content), true, nil
}

func clearStatus(statusDir string, cwd string) error {
	path := filepath.Join(statusDir, EscapeCwd(cwd))

	err := os.Remove(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Ignore if file doesn't exist
			return nil
		}
		return fmt.Errorf("failed to remove status file: %w", err)
	}

	return nil
}
