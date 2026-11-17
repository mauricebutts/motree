package hook

import (
	"encoding/json"
	"io"

	"github.com/mauricebutts/motree/internal/statusfile"
)

// agentSpawnToolName is the tool a running agent (the top-level session or
// a subagent) calls to spin out a nested subagent. Its PreToolUse/
// PostToolUse pair brackets exactly how long that spawning agent is "in
// flight" on a subagent of its own; bracketing it by tool_use_id rather
// than agent_id alone means several concurrent calls (e.g. a batch of
// subagents dispatched in one turn) don't clear each other's marker early.
const agentSpawnToolName = "Agent"

// Run processes a Claude Code hook event from stdin.
// It reads a JSON object with fields hook_event_name, cwd, agent_id,
// tool_name, and tool_use_id, and updates the status file(s) accordingly.
// Unknown events and malformed JSON are silently ignored (no-op).
//
// agent_id is present on every event a subagent fired (its tool calls, its
// own SubagentStop) and absent for the top-level session's own events. A
// subagent runs in the same cwd as its parent, so those events are routed
// to a status file keyed by (cwd, agent_id), separate from the parent's own
// (cwd)-keyed file — without that split, a subagent's SubagentStop landing
// after the parent's own Stop would leave the parent's row stuck showing
// Subagents even once the parent resumes.
//
// Independently of that routing, any event whose tool_name is
// agentSpawnToolName marks (PreToolUse) or clears (PostToolUse) an inflight
// marker for whichever actor (the top-level session or a subagent) issued
// it — tracking, for each actor, whether it is itself currently spinning
// out a nested subagent of its own.
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
		ToolName         string `json:"tool_name"`
		ToolUseID        string `json:"tool_use_id"`
	}

	decoder := json.NewDecoder(stdin)
	if err := decoder.Decode(&payload); err != nil {
		// Malformed JSON is a silent no-op
		return nil
	}

	if payload.ToolName == agentSpawnToolName {
		if err := trackInflight(payload.Cwd, payload.AgentID, payload.ToolUseID, payload.HookEventName); err != nil {
			return err
		}
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

// trackInflight marks or clears the agentSpawnToolName inflight marker for
// actor (cwd, agentID), depending on which half of the tool-call pair event
// is. Every other event is a no-op.
func trackInflight(cwd, agentID, toolUseID, event string) error {
	switch event {
	case "PreToolUse":
		return statusfile.MarkSubagentInflight(cwd, agentID, toolUseID)
	case "PostToolUse":
		return statusfile.ClearSubagentInflight(cwd, agentID, toolUseID)
	default:
		return nil
	}
}
