package hook

import (
	"encoding/json"
	"io"

	"github.com/mauricebutts/motree/internal/statusfile"
)

// Run processes a Claude Code hook event from stdin.
// It reads a JSON object with fields hook_event_name and cwd,
// looks up the event status, and updates the status file accordingly.
// Unknown events, malformed JSON, and subagent-originated events (see
// agent_id below) are silently ignored (no-op).
// I/O errors from statusfile.Write/Clear are propagated to the caller
// (e.g., disk full, permission denied), which main.go logs to stderr.
// main.go always exits 0 regardless of Run's return value.
func Run(stdin io.Reader) error {
	// Parse JSON payload from stdin
	var payload struct {
		HookEventName    string `json:"hook_event_name"`
		Cwd              string `json:"cwd"`
		NotificationType string `json:"notification_type"`
		AgentID          string `json:"agent_id"`
	}

	decoder := json.NewDecoder(stdin)
	if err := decoder.Decode(&payload); err != nil {
		// Malformed JSON is a silent no-op
		return nil
	}

	// agent_id is only present on events a subagent fired (its tool calls,
	// its own SubagentStop) — it's absent for the top-level session's own
	// events. A subagent runs in the same cwd as its parent, so without
	// this check its hooks would stomp the parent's status in the same
	// file: e.g. SubagentStop landing after the parent's own Stop would
	// leave the cwd stuck showing Subagents even once the parent resumes.
	if payload.AgentID != "" {
		return nil
	}

	// Look up the event status
	status, clear, ok := statusfile.EventStatus(payload.HookEventName, payload.NotificationType)
	if !ok {
		// Unknown event is a silent no-op
		return nil
	}

	// Handle the status update, propagating I/O errors
	if clear {
		// SessionEnd event: clear the status file
		return statusfile.Clear(payload.Cwd)
	}
	// Known event: write the status
	return statusfile.Write(payload.Cwd, status)
}
