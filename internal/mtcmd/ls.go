package mtcmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mauricebutts/motree/internal/claudecmd"
	"github.com/mauricebutts/motree/internal/fzfutil"
	"github.com/mauricebutts/motree/internal/state"
	"github.com/mauricebutts/motree/internal/statusfile"
)

// fzfLookPath resolves name to an executable's path, exactly like
// exec.LookPath (its default value). It's a package-level var rather than
// a direct exec.LookPath call so Ls's "fzf isn't installed" fallback is
// deterministically testable, the same trick ghutil.LookPath uses for
// "gh" — reassign the original value back (e.g. via t.Cleanup) after
// overriding it.
var fzfLookPath = exec.LookPath

// previewDivider separates a session's description from its claude pane
// snapshot in Preview's output, matching the original bash motree.sh's
// do_preview divider line exactly.
const previewDivider = "----------------------------------------"

// previewLines is the number of trailing claude-pane lines Preview
// captures, matching the original bash do_preview's "tail -n 28".
const previewLines = 28

// statusEmoji maps each statusfile.Status to the glyph FormatLabel prefixes
// onto a session's name.
var statusEmoji = map[statusfile.Status]string{
	statusfile.Waiting:   "🔔",
	statusfile.Running:   "🏃",
	statusfile.Subagents: "🤖",
	statusfile.Stopped:   "🛑",
}

// formatTimeForLabel formats a LastOpened Unix timestamp exactly as the
// original bash mt_label_for's "date -r "$ts" '+%a %Y-%m-%d %H:%M'" did, in
// local time.
func formatTimeForLabel(lastOpened int64) string {
	return time.Unix(lastOpened, 0).Format("Mon 2006-01-02 15:04")
}

// noDescriptionPlaceholder is shown in place of a session's description,
// both in FormatLabel and in RenderRows' third column, whenever that
// session has none.
const noDescriptionPlaceholder = "(no description)"

// SessionStatus aggregates a session's overall status from the status
// files keyed by each of its repos' worktree paths (the hook writes status
// files keyed by the claude pane's cwd, which for every session this tool
// manages IS the worktree path). Priority, exact from the original bash
// mt_session_status: waiting beats running beats subagents beats stopped.
// found is false only when none of the repos has any status file at all.
func SessionStatus(repos []state.Repo) (status statusfile.Status, found bool) {
	var anyWaiting, anyRunning, anySubagents, anyStopped bool

	for _, repo := range repos {
		s, ok, err := statusfile.Read(repo.WorktreePath)
		if err != nil || !ok {
			continue
		}
		switch s {
		case statusfile.Waiting:
			anyWaiting = true
		case statusfile.Running:
			anyRunning = true
		case statusfile.Subagents:
			anySubagents = true
		case statusfile.Stopped:
			anyStopped = true
		}
	}

	switch {
	case anyWaiting:
		return statusfile.Waiting, true
	case anyRunning:
		return statusfile.Running, true
	case anySubagents:
		return statusfile.Subagents, true
	case anyStopped:
		return statusfile.Stopped, true
	default:
		return "", false
	}
}

// FormatLabel renders the exact row label the original bash mt_label_for
// produces: "<emoji> <name> | <Mon date> (<repos>) — <description>" (no
// trailing space after the name when there's no emoji), where the emoji,
// date, and repo-list segments are each present only under the conditions
// documented on their assembly below, and the description segment is
// always present (showing noDescriptionPlaceholder when sess has none).
func FormatLabel(sess state.Session, status statusfile.Status, found bool) string {
	label := sess.Name
	if found {
		label = statusEmoji[status] + " " + label
	}

	if sess.LastOpened != 0 {
		label += " | " + formatTimeForLabel(sess.LastOpened)
	}

	if len(sess.Repos) > 0 {
		names := make([]string, len(sess.Repos))
		for i, repo := range sess.Repos {
			names[i] = filepath.Base(repo.RepoPath)
		}
		label += " (" + strings.Join(names, ",") + ")"
	}

	desc := sess.Description
	if desc == "" {
		desc = noDescriptionPlaceholder
	}
	label += " — " + desc

	return label
}

// RenderRows renders one "name\tlabel\tdesc" line per session in st, sorted
// most- to least-recently-opened via state.SortedSessions, mirroring the
// original bash mt_render_rows.
func RenderRows(st *state.State) []string {
	sessions := st.SortedSessions()
	rows := make([]string, 0, len(sessions))

	for _, sess := range sessions {
		status, found := SessionStatus(sess.Repos)
		label := FormatLabel(sess, status, found)

		desc := sess.Description
		if desc == "" {
			desc = noDescriptionPlaceholder
		}

		rows = append(rows, sess.Name+"\t"+label+"\t"+desc)
	}

	return rows
}

// Preview produces a one-shot snapshot for the named session: its
// description, alone, if no running claude pane can be found for it (no
// tmux session, no repos recorded, or no claude pane in the window named
// after its first repo) — otherwise that description, a divider line, and
// the last 28 lines of that claude pane's captured text. This is the
// static building block live-reloading preview commands (Task 13) repeat
// on an interval; it does not loop or sleep itself.
func Preview(d *Deps, name string) string {
	var sess *state.Session
	for i := range d.State.Sessions {
		if d.State.Sessions[i].Name == name {
			sess = &d.State.Sessions[i]
			break
		}
	}

	desc := noDescriptionPlaceholder
	if sess != nil && sess.Description != "" {
		desc = sess.Description
	}

	if sess == nil || len(sess.Repos) == 0 {
		return desc
	}

	sessionID, err := d.Tmux.FindSessionID(name)
	if err != nil || sessionID == "" {
		return desc
	}

	windowName := filepath.Base(sess.Repos[0].RepoPath)
	paneIndex, found, err := d.Tmux.FindClaudePane(sessionID, windowName)
	if err != nil || !found {
		return desc
	}

	target := sessionID + ":" + windowName + "." + paneIndex
	paneText, err := d.Tmux.CapturePane(target, previewLines)
	if err != nil {
		return desc
	}

	return desc + "\n" + previewDivider + "\n" + paneText
}

// Ls is the full `mt ls` entrypoint: an fzf picker over tracked sessions,
// optionally live-reloading its rows and preview, that attaches to (or
// recreates) whichever session gets picked. Ported from the original bash
// motree.sh's do_ls.
//
// cwd is accepted for interface symmetry with Create/PR/Spawn's
// entrypoints; do_ls's own behavior never depended on the invoking
// directory, so Ls doesn't use it.
func Ls(d *Deps, mtBinaryPath, cwd string, insideTmux bool) error {
	if len(d.State.SortedSessions()) == 0 {
		fmt.Fprintln(os.Stderr, "mt: no sessions tracked yet")
		return nil
	}

	if _, err := fzfLookPath("fzf"); err != nil {
		for _, sess := range d.State.SortedSessions() {
			status, found := SessionStatus(sess.Repos)
			fmt.Println(FormatLabel(sess, status, found))
			for _, repo := range sess.Repos {
				fmt.Println("  " + repo.RepoPath)
			}
		}
		return nil
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

	args := fzfutil.BuildArgs(mtBinaryPath, listen, socketPath)

	ctx, cancel := context.WithCancel(context.Background())
	if listen {
		fzfutil.StartReloadPoller(ctx, runner, mtBinaryPath, socketPath)
	}
	defer cancel()

	selectedName, runErr := fzfutil.Run(runner, rows, args)
	if runErr != nil {
		return runErr
	}
	if selectedName == "" {
		return nil
	}

	return AttachOrRecreate(d, selectedName, insideTmux)
}

// newFzfSocketPath generates a unique, not-yet-existing path for fzf's
// --listen unix socket, matching the original bash do_ls's
// `mktemp -u "${TMPDIR:-/tmp}/mt-fzf-XXXXXX").sock` — a name, not a file:
// fzf creates/binds the socket itself, so the path must not already exist
// when it starts.
func newFzfSocketPath() (string, error) {
	f, err := os.CreateTemp("", "mt-fzf-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Remove(name); err != nil {
		return "", err
	}
	return name + ".sock", nil
}

// AttachOrRecreate attaches to session name if its tmux session is still
// live, or rebuilds it (the worktree directories already exist — this
// just recreates the tmux session/windows/panes) otherwise, pruning any
// repo whose worktree directory is gone from tracking as it goes. Ported
// from the original bash motree.sh's mt_attach_or_recreate.
func AttachOrRecreate(d *Deps, name string, insideTmux bool) error {
	sessionID, err := d.Tmux.FindSessionID(name)
	if err != nil {
		return err
	}
	if sessionID != "" {
		return d.Tmux.AttachOrSwitch(sessionID, insideTmux)
	}

	var rebuiltSessionID string
	for _, repo := range d.State.ReposFor(name) {
		if _, statErr := os.Stat(repo.WorktreePath); statErr != nil {
			fmt.Fprintf(os.Stderr, "mt: skipping '%s' — worktree is gone, pruning it from tracking\n", repo.RepoPath)
			d.State.RemoveRepo(name, repo.RepoPath)
			continue
		}

		sid, windowTarget, isNewWindow, ensErr := ensureSessionWindow(d, name, filepath.Base(repo.RepoPath), repo.WorktreePath, nil)
		if ensErr != nil {
			return ensErr
		}
		rebuiltSessionID = sid

		if isNewWindow {
			if err := d.Tmux.SendKeys(windowTarget+".2", claudecmd.ResumeCommand(repo.WorktreePath)); err != nil {
				return err
			}
		}
	}

	if rebuiltSessionID == "" {
		return fmt.Errorf("mt: nothing left to recreate for session '%s'", name)
	}

	return d.Tmux.AttachOrSwitch(rebuiltSessionID, insideTmux)
}
