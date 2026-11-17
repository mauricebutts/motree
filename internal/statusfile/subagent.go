package statusfile

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SubagentDir returns the directory holding one status file per (cwd,
// agent_id) pair — every subagent mt is currently tracking, across every
// cwd, at any nesting depth (the hook payload carries no parent-agent
// link, so a subagent spawned by another subagent is tracked exactly like
// one spawned directly by the top-level session — see ListSubagents).
// Sibling to Dir(), which holds one file per cwd for the top-level
// session.
func SubagentDir() string {
	return Dir() + "-subagents"
}

// actorKey is the filename stem for one subagent's status file: cwd and
// agentID, both escaped with EscapeCwd before joining so the "__"
// separator is unambiguous — EscapeCwd already maps every "_" in its input
// to "-", so a literal "__" in the result can only be the separator this
// function inserts, never something that leaked out of cwd or agentID.
func actorKey(cwd, agentID string) string {
	return EscapeCwd(cwd) + "__" + EscapeCwd(agentID)
}

// WriteSubagent atomically writes agentID's status file, scoped to cwd,
// creating the subagent status directory if needed.
func WriteSubagent(cwd, agentID string, status Status) error {
	dir := SubagentDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create subagent status directory: %w", err)
	}
	return atomicWriteFile(filepath.Join(dir, actorKey(cwd, agentID)), []byte(status))
}

// ReadSubagent reads agentID's status, scoped to cwd.
func ReadSubagent(cwd, agentID string) (status Status, found bool, err error) {
	content, err := os.ReadFile(filepath.Join(SubagentDir(), actorKey(cwd, agentID)))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to read subagent status file: %w", err)
	}
	return Status(content), true, nil
}

// ClearSubagent removes agentID's status file, scoped to cwd. SubagentStop
// is that subagent's terminal event — its row should simply disappear
// rather than linger showing a stale status the way the top-level
// session's own file does after Stop.
func ClearSubagent(cwd, agentID string) error {
	return removeIfExists(filepath.Join(SubagentDir(), actorKey(cwd, agentID)))
}

// Subagent is one tracked subagent's resolved display state.
type Subagent struct {
	Status Status
}

// ListSubagents returns the current status of every subagent tracked under
// cwd, in filename order (stable, not otherwise meaningful) — every
// subagent at any nesting depth, since there's no parent-agent link in the
// hook payload to tell a directly-spawned subagent apart from one spawned
// by another subagent. Callers that want "does this cwd have any live
// subagent at all" (e.g. the top-level session's own 🤖 status) just check
// len() > 0; this intentionally does not try to attribute a subagent to
// whichever specific parent spawned it.
func ListSubagents(cwd string) ([]Subagent, error) {
	dir := SubagentDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to list subagent status directory: %w", err)
	}

	prefix := EscapeCwd(cwd) + "__"
	var result []Subagent
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}

		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		result = append(result, Subagent{Status: Status(content)})
	}
	return result, nil
}

// SubagentEventStatus maps a hook event fired with a non-empty agent_id —
// one a subagent itself fired — to a status, clear flag, and ok flag,
// mirroring EventStatus's shape for the top-level session. SubagentStop is
// that subagent's terminal event: clear, not Stopped, since its row should
// disappear entirely rather than linger the way a finished top-level
// session's row does.
func SubagentEventStatus(event, notificationType string) (status Status, clear bool, ok bool) {
	switch event {
	case "SubagentStart", "PreToolUse", "PostToolUse":
		return Running, false, true
	case "Notification":
		return notificationStatus(notificationType)
	case "PermissionRequest":
		return Waiting, false, true
	case "SubagentStop":
		return "", true, true
	default:
		return "", false, false
	}
}

// atomicWriteFile writes content to path via a temp file plus rename, the
// same pattern writeStatus uses.
func atomicWriteFile(path string, content []byte) error {
	tmpPath := path + ".tmp." + fmt.Sprintf("%d%d", time.Now().UnixNano(), rand.Intn(1000000))
	if err := os.WriteFile(tmpPath, content, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}
	return nil
}

// removeIfExists removes path, ignoring a not-exist error.
func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove file: %w", err)
	}
	return nil
}
