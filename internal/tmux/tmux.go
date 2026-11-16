// Package tmux wraps the tmux subprocess calls needed for session/window/
// pane management, mirroring the behavior of the original bash motree
// tool's tmux usage (see docs/superpowers/specs/2026-11-14-motree-go-design.md).
package tmux

import (
	"strconv"
	"strings"

	"github.com/mauricebutts/motree/internal/executil"
)

// Tmux wraps the tmux subprocess calls used for session/window/pane
// management.
type Tmux struct {
	Runner executil.Runner
}

// WindowSize is the initial session size passed to "tmux new-session -x/-y".
// A nil *WindowSize means "let tmux pick its default size".
type WindowSize struct {
	X, Y int
}

// Quote applies POSIX single-quote escaping: it wraps s in single quotes,
// escaping any embedded single quotes, so the result survives unexpanded
// through "tmux send-keys" into the receiving shell regardless of spaces,
// "$", backticks, or double quotes in s.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// FindSessionID resolves name to its tmux session ID by enumerating
// "tmux list-sessions" output and matching by name, rather than ever
// passing a bare name as a "-t" target — tmux reads "." in a "-t name"
// target as a pane separator, so a session name containing "." can't be
// targeted directly. Returns "" (no error) if no session is running at
// all, or if no session matches name.
func (t Tmux) FindSessionID(name string) (string, error) {
	stdout, _, err := t.Runner.Run("tmux", "list-sessions", "-F", "#{session_id}:#{session_name}")
	if err != nil {
		return "", nil
	}
	for _, line := range strings.Split(stdout, "\n") {
		if line == "" {
			continue
		}
		sid, sname, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if sname == name {
			return sid, nil
		}
	}
	return "", nil
}

// NewSession creates a detached session named name, with an initial window
// windowName in cwd, optionally at a fixed size. It returns the new
// session's ID.
func (t Tmux) NewSession(name, windowName, cwd string, size *WindowSize) (string, error) {
	args := []string{"new-session", "-d", "-s", name, "-n", windowName, "-c", cwd}
	if size != nil {
		args = append(args, "-x", strconv.Itoa(size.X), "-y", strconv.Itoa(size.Y))
	}
	args = append(args, "-P", "-F", "#{session_id}")

	stdout, _, err := t.Runner.Run("tmux", args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

// NewWindow creates a new window named windowName in cwd, in the session
// identified by sessionID.
func (t Tmux) NewWindow(sessionID, windowName, cwd string) error {
	_, _, err := t.Runner.Run("tmux", "new-window", "-t", sessionID, "-n", windowName, "-c", cwd)
	return err
}

// WindowExists reports whether a window named windowName exists in the
// session identified by sessionID.
func (t Tmux) WindowExists(sessionID, windowName string) (bool, error) {
	stdout, _, err := t.Runner.Run("tmux", "list-windows", "-t", sessionID, "-F", "#{window_name}")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(stdout, "\n") {
		if line == windowName {
			return true, nil
		}
	}
	return false, nil
}

// SplitThreePane splits windowTarget into the original motree layout: a
// left pane (full height, 75% width) for "nvim .", and a right column
// (25% width) split top/bottom into a terminal pane and a claude pane.
// It only performs the splitting — callers send "nvim ." and the claude
// command/prompt into the resulting panes themselves.
func (t Tmux) SplitThreePane(windowTarget, cwd string) error {
	_, _, err := t.Runner.Run("tmux", "split-window", "-h", "-p", "25", "-t", windowTarget, "-c", cwd)
	if err != nil {
		return err
	}
	_, _, err = t.Runner.Run("tmux", "split-window", "-v", "-t", windowTarget+".1", "-c", cwd)
	return err
}

// SendKeys types keys into target's pane followed by Enter.
func (t Tmux) SendKeys(target, keys string) error {
	_, _, err := t.Runner.Run("tmux", "send-keys", "-t", target, keys, "C-m")
	return err
}

// FindClaudePane returns the index of the first pane in sessionID:windowName
// whose current command is "claude". found is false if no such pane exists
// (including if the session/window can't be inspected at all).
func (t Tmux) FindClaudePane(sessionID, windowName string) (string, bool, error) {
	stdout, _, err := t.Runner.Run("tmux", "list-panes", "-t", sessionID+":"+windowName, "-F", "#{pane_index} #{pane_current_command}")
	if err != nil {
		return "", false, nil
	}
	for _, line := range strings.Split(stdout, "\n") {
		if line == "" {
			continue
		}
		index, command, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if command == "claude" {
			return index, true, nil
		}
	}
	return "", false, nil
}

// CapturePane returns the last lastNLines lines of target's rendered pane
// text.
func (t Tmux) CapturePane(target string, lastNLines int) (string, error) {
	stdout, _, err := t.Runner.Run("tmux", "capture-pane", "-t", target, "-p")
	if err != nil {
		return "", err
	}
	lines := strings.Split(stdout, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if lastNLines >= 0 && len(lines) > lastNLines {
		lines = lines[len(lines)-lastNLines:]
	}
	return strings.Join(lines, "\n"), nil
}

// KillSession kills the session identified by sessionID.
func (t Tmux) KillSession(sessionID string) error {
	_, _, err := t.Runner.Run("tmux", "kill-session", "-t", sessionID)
	return err
}

// AttachOrSwitch attaches to the session identified by sessionID, using
// "switch-client" if already inside a tmux client (insideTmux), or
// "attach-session" otherwise.
//
// The two branches go through different Runner methods on purpose.
// "switch-client" only messages the already-running tmux server, so the
// ordinary captured-output Run is fine. "attach-session" instead needs our
// controlling terminal: its stdin must be a real tty, or tmux exits with
// "open terminal failed: not a terminal" — so it goes through
// RunInteractive, which inherits this process's real stdio.
//
// Either way this call takes the terminal over (until the user detaches or
// switches away), so callers must persist any state they care about
// *before* calling it — see mtcmd.saveAndAttach.
func (t Tmux) AttachOrSwitch(sessionID string, insideTmux bool) error {
	if insideTmux {
		_, _, err := t.Runner.Run("tmux", "switch-client", "-t", sessionID)
		return err
	}
	return t.Runner.RunInteractive("tmux", "attach-session", "-t", sessionID)
}
