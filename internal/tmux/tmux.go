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

// DisplayMessage runs "tmux display-message -p -t <target> <format>" and
// returns its trimmed stdout — used for #{pane_tty} and
// #{window_zoomed_flag} queries against a specific pane/window.
func (t Tmux) DisplayMessage(target, format string) (string, error) {
	stdout, _, err := t.Runner.Run("tmux", "display-message", "-p", "-t", target, format)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

// DisplayMessageCurrent runs "tmux display-message -p <format>" with no
// "-t", i.e. against whichever session/pane this process is attached to —
// used for `mt rm --self`'s current-session-name lookup ("#S"). Kept
// separate from DisplayMessage (rather than letting target be empty there)
// so an accidentally-empty target elsewhere is a visible bug, not a
// silently-valid untargeted query.
func (t Tmux) DisplayMessageCurrent(format string) (string, error) {
	stdout, _, err := t.Runner.Run("tmux", "display-message", "-p", format)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

// ShowMessage runs "tmux display-message -t <target> <text>" (no "-p"),
// surfacing text as tmux's own status-line message in target's pane —
// used by Dock's two "nothing to show" cases.
func (t Tmux) ShowMessage(target, text string) error {
	_, _, err := t.Runner.Run("tmux", "display-message", "-t", target, text)
	return err
}

// SplitRightPane runs "tmux split-window -h -p <percent> -t <windowTarget>
// -c <cwd>": a generic two-pane horizontal split, unlike the hard-coded
// 3-pane SplitThreePane above. Used to build the command center's
// picker/mirror pane pair.
func (t Tmux) SplitRightPane(windowTarget, cwd string, percent int) error {
	_, _, err := t.Runner.Run("tmux", "split-window", "-h", "-p", strconv.Itoa(percent), "-t", windowTarget, "-c", cwd)
	return err
}

// SessionOption runs "tmux show-option -t <sessionID> -v <name>" and
// returns its trimmed stdout, which is empty (not an error) when the
// option is unset.
func (t Tmux) SessionOption(sessionID, name string) (string, error) {
	stdout, _, err := t.Runner.Run("tmux", "show-option", "-t", sessionID, "-v", name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

// SetSessionOption runs "tmux set-option -t <sessionID> <name> <value>".
func (t Tmux) SetSessionOption(sessionID, name, value string) error {
	_, _, err := t.Runner.Run("tmux", "set-option", "-t", sessionID, name, value)
	return err
}

// ListClientSessions runs "tmux list-clients -F '#{client_tty}
// #{session_id}'" and returns a tty->sessionID map. Returns an empty map
// (never an error) when there are no clients, or no tmux server at all —
// the same "degrade to empty" convention FindSessionID already uses.
func (t Tmux) ListClientSessions() (map[string]string, error) {
	result := map[string]string{}
	stdout, _, err := t.Runner.Run("tmux", "list-clients", "-F", "#{client_tty} #{session_id}")
	if err != nil {
		return result, nil
	}
	for _, line := range strings.Split(stdout, "\n") {
		if line == "" {
			continue
		}
		tty, sid, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		result[tty] = sid
	}
	return result, nil
}

// SwitchClientTo runs "tmux switch-client -c <tty> -t <sessionID>",
// retargeting the client attached at tty — distinct from AttachOrSwitch,
// which always means "this process's own controlling client".
func (t Tmux) SwitchClientTo(tty, sessionID string) error {
	_, _, err := t.Runner.Run("tmux", "switch-client", "-c", tty, "-t", sessionID)
	return err
}

// SelectWindow runs "tmux select-window -t <target>".
func (t Tmux) SelectWindow(target string) error {
	_, _, err := t.Runner.Run("tmux", "select-window", "-t", target)
	return err
}

// SelectPane runs "tmux select-pane -t <target>" (bare, no "-Z").
func (t Tmux) SelectPane(target string) error {
	_, _, err := t.Runner.Run("tmux", "select-pane", "-t", target)
	return err
}

// PanePID returns the pid of the pane identified by target, a
// "<window>.<index>" string (e.g. "$3:cmd.0") — split on the last "."
// into the window part and the pane index, same convention used
// elsewhere in this codebase for assembling (never, until now,
// disassembling) a full pane target. Returns "" (no error) if target has
// no matching pane, including if the window can't be inspected at all.
func (t Tmux) PanePID(target string) (string, error) {
	windowTarget, index, ok := cutLastDot(target)
	if !ok {
		return "", nil
	}

	stdout, _, err := t.Runner.Run("tmux", "list-panes", "-t", windowTarget, "-F", "#{pane_index} #{pane_pid}")
	if err != nil {
		return "", nil
	}
	for _, line := range strings.Split(stdout, "\n") {
		if line == "" {
			continue
		}
		paneIndex, pid, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if paneIndex == index {
			return pid, nil
		}
	}
	return "", nil
}

// cutLastDot splits s at its last ".", mirroring tmux's own pane-separator
// convention (a window target's pane index is everything after the final
// "."). ok is false if s has no ".".
func cutLastDot(s string) (before, after string, ok bool) {
	i := strings.LastIndex(s, ".")
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// HasChildProcess runs "pgrep -P <pid>" and reports whether it exited 0
// (meaning pid has at least one running child process). Never returns an
// error: a failed/erroring pgrep means "no child", matching bash's
// `pgrep ... >/dev/null 2>&1` being checked only for its exit status.
func (t Tmux) HasChildProcess(pid string) bool {
	_, _, err := t.Runner.Run("pgrep", "-P", pid)
	return err == nil
}

// ZoomOff un-zooms windowTarget's pane if it's currently zoomed, porting
// the original bash mt_zoom_off exactly: a no-op if windowTarget is empty
// or its zoomed-flag can't be queried, and a toggle (tmux resize-pane -Z)
// only when the flag reads "1".
func (t Tmux) ZoomOff(windowTarget string) error {
	return t.toggleZoom(windowTarget, "1")
}

// ZoomOn zooms windowTarget's pane if it isn't already, porting the
// original bash mt_zoom_on exactly: a no-op if windowTarget is empty or
// its zoomed-flag can't be queried, and a toggle (tmux resize-pane -Z)
// only when the flag reads "0".
func (t Tmux) ZoomOn(windowTarget string) error {
	return t.toggleZoom(windowTarget, "0")
}

// toggleZoom reads windowTarget's #{window_zoomed_flag} and, only if it
// equals triggerFlag, runs "tmux resize-pane -Z -t <windowTarget>" to
// toggle it. A query error is treated as "nothing to toggle" (returns
// nil), matching bash's "|| return 0" on the same query in both
// mt_zoom_off and mt_zoom_on.
func (t Tmux) toggleZoom(windowTarget, triggerFlag string) error {
	if windowTarget == "" {
		return nil
	}
	flag, err := t.DisplayMessage(windowTarget, "#{window_zoomed_flag}")
	if err != nil {
		return nil
	}
	if flag != triggerFlag {
		return nil
	}
	_, _, err = t.Runner.Run("tmux", "resize-pane", "-Z", "-t", windowTarget)
	return err
}
