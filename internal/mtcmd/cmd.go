package mtcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mauricebutts/motree/internal/fzfutil"
	"github.com/mauricebutts/motree/internal/tmux"
)

// CmdSessionName is the reserved tmux session name for `mt cmd`'s command
// center — deliberately obscure (never a name a real worktree session
// would have; a plainer "cmd-center" has collided with a real tracked
// session on this tool's own machine before).
const CmdSessionName = "__mt_cmd_center__"

// cmdDockedOption is the tmux session option on CmdSessionName that
// tracks which tracked session's claude pane is currently mirrored into
// the command center's right pane.
const cmdDockedOption = "@mt_cmd_docked"

// WindowTargetFor resolves name's window target — its tmux session ID,
// plus the window named after its first tracked repo's basename — for
// use as a `tmux select-window`/`select-pane` target. found is false if
// name has no live tmux session, or has one but no tracked repos. Ported
// from the original bash mt_window_target_for, session-liveness checked
// before repos (not the reverse) so a session with no running tmux
// session at all short-circuits before even looking at its repos.
func WindowTargetFor(d *Deps, name string) (target string, found bool) {
	sessionID, err := d.Tmux.FindSessionID(name)
	if err != nil || sessionID == "" {
		return "", false
	}

	repos := d.State.ReposFor(name)
	if len(repos) == 0 {
		return "", false
	}

	return sessionID + ":" + filepath.Base(repos[0].RepoPath), true
}

// Dock makes the command center's right pane (CmdSessionName's "cmd"
// window, pane index 1) mirror targetName's claude pane. Ported from the
// original bash do_cmd_dock.
//
// Dock returns a non-nil error only when it can't even locate the running
// command center itself (callers only ever reach __mt_cmd_dock through
// its own fzf picker, which can't exist without that session, so this is
// a defensive case, not an expected one). Every other "can't proceed"
// case — no live session for targetName, no claude pane — reports via
// ShowMessage into the right pane and returns nil, matching bash's
// `do_cmd_dock ... || true` throughout: a failed tmux call along the way
// (e.g. a stale pane disappearing mid-dock) is likewise swallowed rather
// than surfaced, exactly as bash's own `|| true` suffixes do.
func Dock(d *Deps, mtBinaryPath, targetName string) error {
	cmdSessionID, err := d.Tmux.FindSessionID(CmdSessionName)
	if err != nil {
		return err
	}
	if cmdSessionID == "" {
		return fmt.Errorf("command center isn't running")
	}
	rightPane := cmdSessionID + ":cmd.1"

	targetWindow, found := WindowTargetFor(d, targetName)
	if !found {
		_ = d.Tmux.ShowMessage(rightPane, fmt.Sprintf("mt cmd: '%s' has no running session", targetName))
		return nil
	}
	targetSessionID, windowName, _ := cutFirstColon(targetWindow)

	claudePane, found, err := d.Tmux.FindClaudePane(targetSessionID, windowName)
	if err != nil {
		return err
	}
	if !found {
		_ = d.Tmux.ShowMessage(rightPane, fmt.Sprintf("mt cmd: no claude pane running for '%s'", targetName))
		return nil
	}

	rightTty, _ := d.Tmux.DisplayMessage(rightPane, "#{pane_tty}")
	clients, _ := d.Tmux.ListClientSessions()
	mirrorSessionID := clients[rightTty]

	if mirrorSessionID == targetSessionID {
		_ = d.Tmux.SelectPane(rightPane)
		return nil
	}

	prevName, _ := d.Tmux.SessionOption(cmdSessionID, cmdDockedOption)
	if prevName != "" && prevName != targetName {
		prevWindow, _ := WindowTargetFor(d, prevName)
		_ = d.Tmux.ZoomOff(prevWindow)
	}

	_ = d.Tmux.SelectWindow(targetWindow)
	_ = d.Tmux.SelectPane(targetWindow + "." + claudePane)
	_ = d.Tmux.ZoomOn(targetWindow)

	if mirrorSessionID == "" {
		_ = d.Tmux.SendKeys(rightPane, "TMUX= tmux attach -t "+tmux.Quote(targetSessionID))
	} else {
		_ = d.Tmux.SwitchClientTo(rightTty, targetSessionID)
	}

	_ = d.Tmux.SetSessionOption(cmdSessionID, cmdDockedOption, targetName)
	_ = d.Tmux.SelectPane(rightPane)

	return nil
}

// cutFirstColon splits s at its first ":", matching the "<sessionID>:
// <windowName>" shape WindowTargetFor produces (a tmux session ID never
// contains ":", so the first colon unambiguously separates it from the
// window name that follows).
func cutFirstColon(s string) (before, after string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// cmdListFzfArgs builds `__mt_cmd_list`'s fzf argv, ported directly from
// the original bash do_cmd_list's fzf invocation:
//
//	fzf --delimiter='\t' --with-nth=2 --height=100% --reverse --track --id-nth=1 $listen_flag \
//	  --bind "enter:execute-silent('$script_path' __mt_cmd_dock {1})" \
//	  --bind "ctrl-c:execute-silent('$script_path' __mt_cmd_quit)+abort" \
//	  --header='enter: show the selected claude pane on the right  ·  ctrl-c: quit command center'
//
// mtBinaryPath is quoted (via tmux.Quote) in place of the original's
// "$script_path". socketPath is ignored when listen is false.
func cmdListFzfArgs(mtBinaryPath string, listen bool, socketPath string) []string {
	args := []string{
		`--delimiter=\t`,
		"--with-nth=2",
		"--height=100%",
		"--reverse",
		"--track",
		"--id-nth=1",
	}

	if listen {
		args = append(args, "--listen="+socketPath)
	}

	quoted := tmux.Quote(mtBinaryPath)
	args = append(args,
		"--bind=enter:execute-silent("+quoted+" __mt_cmd_dock {1})",
		"--bind=ctrl-c:execute-silent("+quoted+" __mt_cmd_quit)+abort",
		"--header=enter: show the selected claude pane on the right  ·  ctrl-c: quit command center",
	)

	return args
}

// List runs `__mt_cmd_list`: an fzf picker over tracked sessions, live-
// reloaded the same way `mt ls` is, whose enter/ctrl-c bindings drive
// Dock/Quit directly via execute-silent — List itself never looks at
// which row (if any) fzf reports selected, since every action it causes
// already happened through those bindings while fzf was running. Ported
// from the original bash do_cmd_list.
func List(d *Deps, mtBinaryPath string) error {
	if len(d.State.SortedSessions()) == 0 {
		fmt.Fprintln(os.Stderr, "mt: no sessions tracked yet")
		return nil
	}

	if _, err := fzfLookPath("fzf"); err != nil {
		return errors.New("mt cmd needs fzf installed.")
	}

	rows := RenderRows(d.State)
	runner := d.Tmux.Runner

	listen := fzfutil.SupportsListen(runner)

	var socketPath string
	if listen {
		sp, err := newFzfSocketPath()
		if err != nil {
			return err
		}
		socketPath = sp
		defer os.Remove(socketPath)
	}

	args := cmdListFzfArgs(mtBinaryPath, listen, socketPath)

	ctx, cancel := context.WithCancel(context.Background())
	var waitForPoller func()
	if listen {
		waitForPoller = fzfutil.StartReloadPoller(ctx, mtBinaryPath, socketPath)
	}

	_, runErr := fzfutil.Run(runner, rows, args)
	cancel()
	if waitForPoller != nil {
		waitForPoller()
	}
	return runErr
}

// Quit tears down the command center: un-zooms whatever session is
// currently docked (if any), then kills CmdSessionName itself. A no-op if
// the command center isn't running. Ported from the original bash
// do_cmd_quit.
func Quit(d *Deps) error {
	cmdSessionID, err := d.Tmux.FindSessionID(CmdSessionName)
	if err != nil {
		return err
	}
	if cmdSessionID == "" {
		return nil
	}

	dockedName, _ := d.Tmux.SessionOption(cmdSessionID, cmdDockedOption)
	if dockedName != "" {
		dockedWindow, _ := WindowTargetFor(d, dockedName)
		_ = d.Tmux.ZoomOff(dockedWindow)
	}

	return d.Tmux.KillSession(cmdSessionID)
}

// cmdWindowSize is the fixed size passed to "tmux new-session" when
// creating the command center, matching the original bash do_cmd's
// "-x 200 -y 50".
var cmdWindowSize = &tmux.WindowSize{X: 200, Y: 50}

// Cmd is the full `mt cmd` entrypoint: finds or creates the command
// center (CmdSessionName's "cmd" window, with the picker in pane 0 and
// the mirror pane at index 1), then attaches to it. Ported from the
// original bash do_cmd.
//
// On first creation, the new window's cwd is this process's own working
// directory (os.Getwd) — the original bash `tmux new-session` here omits
// "-c" entirely, which makes tmux default the session's cwd to the
// invoking process's own cwd; os.Getwd is that same default, reached
// explicitly because this port's NewSession always takes a cwd argument.
func Cmd(d *Deps, mtBinaryPath string, insideTmux bool) error {
	sessionID, err := d.Tmux.FindSessionID(CmdSessionName)
	if err != nil {
		return err
	}

	if sessionID != "" && len(d.State.ReposFor(CmdSessionName)) > 0 {
		return fmt.Errorf("a tmux session named '%s' exists and is tracked as a real worktree session — refusing to touch it. This shouldn't happen; if it does, something is using that reserved name.", CmdSessionName)
	}

	pickerCmd := tmux.Quote(mtBinaryPath) + " __mt_cmd_list"

	if sessionID == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		sessionID, err = d.Tmux.NewSession(CmdSessionName, "cmd", cwd, cmdWindowSize)
		if err != nil {
			return err
		}

		windowTarget := sessionID + ":cmd"
		if err := d.Tmux.SplitRightPane(windowTarget, os.Getenv("HOME"), 70); err != nil {
			return err
		}
		if err := d.Tmux.SendKeys(windowTarget+".0", pickerCmd); err != nil {
			return err
		}
		if err := d.Tmux.SelectPane(windowTarget + ".0"); err != nil {
			return err
		}
	} else {
		windowTarget := sessionID + ":cmd"
		pid, err := d.Tmux.PanePID(windowTarget + ".0")
		if err != nil {
			return err
		}
		if pid == "" || !d.Tmux.HasChildProcess(pid) {
			if err := d.Tmux.SendKeys(windowTarget+".0", pickerCmd); err != nil {
				return err
			}
		}
	}

	return d.Tmux.AttachOrSwitch(sessionID, insideTmux)
}
