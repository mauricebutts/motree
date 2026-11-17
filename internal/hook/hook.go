package hook

import (
	"encoding/json"
	"io"

	"github.com/mauricebutts/motree/internal/statusfile"
)

// Run processes a Claude Code hook event from stdin.
// It reads a JSON object with fields hook_event_name and cwd,
// looks up the event status, and updates the status file accordingly.
// Unknown events and malformed JSON are silently ignored (no-op).
// I/O errors from statusfile.Write/Clear are propagated to the caller
// (e.g., disk full, permission denied), which main.go logs to stderr.
// main.go always exits 0 regardless of Run's return value.
func Run(stdin io.Reader) error {
	// Parse JSON payload from stdin
	var payload struct {
		HookEventName string `json:"hook_event_name"`
		Cwd           string `json:"cwd"`
	}

	decoder := json.NewDecoder(stdin)
	if err := decoder.Decode(&payload); err != nil {
		// Malformed JSON is a silent no-op
		return nil
	}

	// Look up the event status
	status, clear, ok := statusfile.EventStatus(payload.HookEventName)
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
