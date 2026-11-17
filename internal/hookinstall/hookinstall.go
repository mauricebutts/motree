// Package hookinstall registers mt's "__hook" subcommand with Claude Code
// by editing a settings.json file (normally ~/.claude/settings.json).
package hookinstall

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

// HookEvents lists every Claude Code hook event that mt's __hook
// subcommand should be registered against.
var HookEvents = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse",
	"Notification", "PermissionRequest", "SubagentStop", "Stop",
	"StopFailure", "SessionEnd",
}

// Merge adds a hook entry invoking "<mtBinaryPath> __hook" for every event
// in HookEvents to the settings file at settingsPath. Existing hook groups
// (for mt's own command or any other tool's) and unrelated top-level keys
// are left untouched. Merge is idempotent: if a group with the exact
// command "<mtBinaryPath> __hook" already exists for an event, no
// duplicate is added. The file is written atomically via a temp file plus
// rename.
func Merge(settingsPath, mtBinaryPath string) error {
	command := mtBinaryPath + " __hook"

	settings, err := readSettings(settingsPath)
	if err != nil {
		return err
	}

	hooks, err := hooksObject(settings)
	if err != nil {
		return err
	}

	for _, event := range HookEvents {
		groups, err := hookGroups(hooks, event)
		if err != nil {
			return err
		}

		if hasCommand(groups, command) {
			continue
		}

		groups = append(groups, newHookGroup(command))
		hooks[event] = groups
	}
	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	return writeFileAtomic(settingsPath, out)
}

// readSettings reads and parses settingsPath, returning an empty settings
// object if the file does not exist or is empty.
func readSettings(settingsPath string) (map[string]interface{}, error) {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]interface{}{}, nil
		}
		return nil, fmt.Errorf("failed to read settings file: %w", err)
	}
	if len(data) == 0 {
		return map[string]interface{}{}, nil
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("failed to parse settings file: %w", err)
	}
	return settings, nil
}

// hooksObject returns the "hooks" object from settings, creating an empty
// one if absent.
func hooksObject(settings map[string]interface{}) (map[string]interface{}, error) {
	raw, ok := settings["hooks"]
	if !ok {
		return map[string]interface{}{}, nil
	}
	hooks, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("settings file has invalid \"hooks\" field (not an object)")
	}
	return hooks, nil
}

// hookGroups returns the array of hook groups registered for event,
// or nil if none are registered yet.
func hookGroups(hooks map[string]interface{}, event string) ([]interface{}, error) {
	raw, ok := hooks[event]
	if !ok {
		return nil, nil
	}
	groups, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("settings file has invalid hooks.%s field (not an array)", event)
	}
	return groups, nil
}

// hasCommand reports whether any hook group in groups already contains an
// entry whose command exactly matches command.
func hasCommand(groups []interface{}, command string) bool {
	for _, g := range groups {
		group, ok := g.(map[string]interface{})
		if !ok {
			continue
		}
		hookList, ok := group["hooks"].([]interface{})
		if !ok {
			continue
		}
		for _, h := range hookList {
			entry, ok := h.(map[string]interface{})
			if !ok {
				continue
			}
			if cmd, ok := entry["command"].(string); ok && cmd == command {
				return true
			}
		}
	}
	return false
}

// newHookGroup builds a single hook group of the shape Claude Code expects:
// {"hooks": [{"type": "command", "command": command}]}
func newHookGroup(command string) map[string]interface{} {
	return map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": command,
			},
		},
	}
}

// writeFileAtomic writes data to path via a temp file plus rename, creating
// path's parent directory if needed.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create settings directory: %w", err)
	}

	tmpPath := path + ".tmp." + fmt.Sprintf("%d%d", time.Now().UnixNano(), rand.Intn(1000000))
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp settings file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp settings file: %w", err)
	}

	return nil
}
