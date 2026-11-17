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
// cwd. Sibling to Dir(), which holds one file per cwd for the top-level
// session.
func SubagentDir() string {
	return Dir() + "-subagents"
}

// subagentInflightDir returns the directory holding one empty marker file
// per (cwd, agent_id, tool_use_id) for every Agent-tool call currently in
// flight — i.e. every actor (the top-level session or a subagent) that is
// itself in the middle of spinning out a nested subagent.
func subagentInflightDir() string {
	return Dir() + "-subagents-inflight"
}

// actorKey is the filename stem shared by one actor's subagent status file
// and its inflight markers: the top-level session when agentID is "", or
// one specific subagent otherwise, scoped to cwd. Escaping both halves with
// EscapeCwd before joining them keeps the "__" separator unambiguous —
// EscapeCwd already maps every "_" in its input to "-", so a literal "__"
// in the result can only be the separator this function inserts, never
// something that leaked out of cwd or agentID.
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

// ReadSubagent reads agentID's status, scoped to cwd, applying the same
// inflight override ListSubagents does.
func ReadSubagent(cwd, agentID string) (status Status, found bool, err error) {
	key := actorKey(cwd, agentID)
	content, err := os.ReadFile(filepath.Join(SubagentDir(), key))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to read subagent status file: %w", err)
	}

	status = Status(content)
	if hasInflightForKey(key) {
		status = Subagents
	}
	return status, true, nil
}

// ClearSubagent removes agentID's status file (scoped to cwd) and every
// inflight marker left over for it. SubagentStop is that subagent's
// terminal event — its row should simply disappear, markers and all,
// rather than linger showing a stale status the way the top-level
// session's own file does after Stop.
func ClearSubagent(cwd, agentID string) error {
	key := actorKey(cwd, agentID)
	if err := removeIfExists(filepath.Join(SubagentDir(), key)); err != nil {
		return err
	}
	return clearInflightForKey(key)
}

// Subagent is one tracked subagent's resolved display state.
type Subagent struct {
	Status Status
}

// ListSubagents returns the current display state of every subagent
// tracked under cwd, in filename order (stable, not otherwise meaningful).
// A subagent with any Agent-tool call in flight is reported as Subagents
// (🤖) regardless of its last-written status — it's itself in the middle of
// spinning out a nested subagent, which takes priority over whatever tool
// call it was last seen running.
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

		status := Status(content)
		if hasInflightForKey(entry.Name()) {
			status = Subagents
		}
		result = append(result, Subagent{Status: status})
	}
	return result, nil
}

// MarkSubagentInflight records that actor (cwd, agentID) has just issued an
// Agent-tool call identified by toolUseID that hasn't returned yet.
func MarkSubagentInflight(cwd, agentID, toolUseID string) error {
	dir := subagentInflightDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create subagent inflight directory: %w", err)
	}
	return atomicWriteFile(filepath.Join(dir, inflightName(cwd, agentID, toolUseID)), nil)
}

// ClearSubagentInflight records that the Agent-tool call toolUseID actor
// (cwd, agentID) issued has returned.
func ClearSubagentInflight(cwd, agentID, toolUseID string) error {
	return removeIfExists(filepath.Join(subagentInflightDir(), inflightName(cwd, agentID, toolUseID)))
}

// HasSubagentInflight reports whether actor (cwd, agentID) currently has
// any Agent-tool call in flight.
func HasSubagentInflight(cwd, agentID string) (bool, error) {
	return hasInflightForKeyChecked(actorKey(cwd, agentID))
}

func inflightName(cwd, agentID, toolUseID string) string {
	return actorKey(cwd, agentID) + "__" + EscapeCwd(toolUseID)
}

// hasInflightForKey is the best-effort form ReadSubagent/ListSubagents use
// while rendering: a read error (missing directory, a racing remove) is
// treated as "no inflight call", the same no-fuss tolerance Read already
// applies to a missing status file.
func hasInflightForKey(key string) bool {
	has, _ := hasInflightForKeyChecked(key)
	return has
}

func hasInflightForKeyChecked(key string) (bool, error) {
	entries, err := os.ReadDir(subagentInflightDir())
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to list subagent inflight directory: %w", err)
	}

	prefix := key + "__"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			return true, nil
		}
	}
	return false, nil
}

// clearInflightForKey removes every inflight marker belonging to key (used
// when the actor itself goes away via ClearSubagent, so a crash mid-nested
// spawn doesn't leave an orphaned marker behind).
func clearInflightForKey(key string) error {
	dir := subagentInflightDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to list subagent inflight directory: %w", err)
	}

	prefix := key + "__"
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if err := removeIfExists(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
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
