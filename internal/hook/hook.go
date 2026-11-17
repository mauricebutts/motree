package hook

import (
	"encoding/json"
	"io"

	"github.com/mauricebutts/motree/internal/statusfile"
)

// Run processes a Claude Code hook event from stdin.
// It reads a JSON object with fields hook_event_name, cwd, and agent_id,
// and updates the status file(s) accordingly. Unknown events and malformed
// JSON are silently ignored (no-op).
//
// agent_id is present on every event a subagent fired (its tool calls, its
// own SubagentStop) and absent for the top-level session's own events. A
// subagent runs in the same cwd as its parent, so those events are routed
// to a status file keyed by (cwd, agent_id), separate from the parent's own
// (cwd)-keyed file — without that split, a subagent's SubagentStop landing
// after the parent's own Stop would leave the parent's row stuck showing
// Subagents even once the parent resumes.
//
// I/O errors from statusfile's Write/Clear functions are propagated to the
// caller (e.g., disk full, permission denied), which main.go logs to
// stderr. main.go always exits 0 regardless of Run's return value.
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

	if payload.AgentID != "" {
		status, clear, ok := statusfile.SubagentEventStatus(payload.HookEventName, payload.NotificationType)
		if !ok {
			// Unknown event is a silent no-op
			return nil
		}
		if clear {
			// SubagentStop: that subagent's row disappears entirely
			return statusfile.ClearSubagent(payload.Cwd, payload.AgentID)
		}
		return statusfile.WriteSubagent(payload.Cwd, payload.AgentID, status)
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
