package hookinstall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const fixtureSettings = `{
  "attribution": { "commit": "", "pr": "" },
  "permissions": { "blockReadsOutsideWorkingDirectories": true },
  "hooks": {
    "Notification": [
      { "hooks": [ { "type": "command", "command": "/Users/x/.config/iterm2/cc-status" } ] }
    ],
    "PreToolUse": [
      { "hooks": [ { "type": "command", "command": "/Users/x/.config/iterm2/cc-status" } ] }
    ]
  }
}`

const mtBinaryPath = "/usr/local/bin/mt"

// writeFixture writes the fixture settings.json into a fresh t.TempDir()
// and returns its path. Tests must never touch the real ~/.claude/settings.json.
func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(fixtureSettings), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	return path
}

// loadSettings reads and parses the settings file at path.
func loadSettings(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read settings file: %v", err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings file: %v", err)
	}
	return settings
}

// commandsFor returns every "command" string found across the hook groups
// for the given event.
func commandsFor(t *testing.T, settings map[string]interface{}, event string) []string {
	t.Helper()
	hooks, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		t.Fatalf("settings missing \"hooks\" object")
	}
	groupsRaw, ok := hooks[event]
	if !ok {
		return nil
	}
	groups, ok := groupsRaw.([]interface{})
	if !ok {
		t.Fatalf("hooks.%s is not an array", event)
	}

	var commands []string
	for _, g := range groups {
		group := g.(map[string]interface{})
		hookList := group["hooks"].([]interface{})
		for _, h := range hookList {
			hookEntry := h.(map[string]interface{})
			commands = append(commands, hookEntry["command"].(string))
		}
	}
	return commands
}

func countOccurrences(items []string, target string) int {
	count := 0
	for _, item := range items {
		if item == target {
			count++
		}
	}
	return count
}

func contains(items []string, target string) bool {
	return countOccurrences(items, target) > 0
}

func TestHookEvents_IncludesSubagentStart(t *testing.T) {
	if !contains(HookEvents, "SubagentStart") {
		t.Errorf("HookEvents = %v, want it to include %q", HookEvents, "SubagentStart")
	}
}

func TestMerge_AddsHookForEveryEventWithoutExisting(t *testing.T) {
	path := writeFixture(t)

	if err := Merge(path, mtBinaryPath); err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}

	settings := loadSettings(t, path)
	wantCommand := mtBinaryPath + " __hook"

	for _, event := range HookEvents {
		commands := commandsFor(t, settings, event)
		if !contains(commands, wantCommand) {
			t.Errorf("event %q: missing hook command %q, got %v", event, wantCommand, commands)
		}
	}
}

func TestMerge_PreservesExistingITerm2Entries(t *testing.T) {
	path := writeFixture(t)

	if err := Merge(path, mtBinaryPath); err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}

	settings := loadSettings(t, path)
	const ccStatus = "/Users/x/.config/iterm2/cc-status"

	for _, event := range []string{"Notification", "PreToolUse"} {
		commands := commandsFor(t, settings, event)
		if !contains(commands, ccStatus) {
			t.Errorf("event %q: expected existing cc-status entry to survive, got %v", event, commands)
		}
	}
}

func TestMerge_PreservesUnrelatedTopLevelKeys(t *testing.T) {
	path := writeFixture(t)

	if err := Merge(path, mtBinaryPath); err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}

	settings := loadSettings(t, path)

	attribution, ok := settings["attribution"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected \"attribution\" key to survive as an object, got %v", settings["attribution"])
	}
	if attribution["commit"] != "" || attribution["pr"] != "" {
		t.Errorf("attribution contents changed: %v", attribution)
	}

	permissions, ok := settings["permissions"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected \"permissions\" key to survive as an object, got %v", settings["permissions"])
	}
	if permissions["blockReadsOutsideWorkingDirectories"] != true {
		t.Errorf("permissions contents changed: %v", permissions)
	}
}

func TestMerge_IdempotentOnSecondRun(t *testing.T) {
	path := writeFixture(t)

	if err := Merge(path, mtBinaryPath); err != nil {
		t.Fatalf("first Merge returned error: %v", err)
	}
	if err := Merge(path, mtBinaryPath); err != nil {
		t.Fatalf("second Merge returned error: %v", err)
	}

	settings := loadSettings(t, path)
	wantCommand := mtBinaryPath + " __hook"

	for _, event := range HookEvents {
		commands := commandsFor(t, settings, event)
		if count := countOccurrences(commands, wantCommand); count != 1 {
			t.Errorf("event %q: expected exactly 1 occurrence of %q, got %d (%v)", event, wantCommand, count, commands)
		}
	}

	// The iTerm2 entries must also survive a second run untouched/undoubled.
	const ccStatus = "/Users/x/.config/iterm2/cc-status"
	for _, event := range []string{"Notification", "PreToolUse"} {
		commands := commandsFor(t, settings, event)
		if count := countOccurrences(commands, ccStatus); count != 1 {
			t.Errorf("event %q: expected exactly 1 occurrence of %q, got %d (%v)", event, ccStatus, count, commands)
		}
	}
}
