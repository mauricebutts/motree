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
// snapshot in Preview's output.
const previewDivider = "----------------------------------------"

// previewLines is the number of trailing claude-pane lines Preview
// captures.
const previewLines = 28

// statusEmoji maps each statusfile.Status to the glyph FormatLabel prefixes
// onto a session's name.
var statusEmoji = map[statusfile.Status]string{
	statusfile.Waiting:   "🔔",
	statusfile.Running:   "🏃",
	statusfile.Subagents: "🤖",
	statusfile.Stopped:   "🛑",
}

// formatTimeForLabel formats a LastOpened Unix timestamp as
// "<abbreviated weekday> <YYYY-MM-DD> <HH:MM>" in local time.
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
// manages IS the worktree path). Priority: waiting beats running beats
// subagents beats stopped. found is false only when none of the repos has
// any status file at all.
func SessionStatus(repos []state.Repo) (status statusfile.Status, found bool) {
	var anyWaiting, anyRunning, anySubagents, anyStopped bool

	for _, repo := range repos {
		s, ok, err := statusfile.Read(repo.WorktreePath)
		if err != nil || !ok {
			continue
		}
		// A session with any live subagent at all (at any nesting depth —
		// there's no parent-agent link to tell them apart) is, by
		// definition, currently managing one, which takes priority over
		// whatever status its last plain hook event wrote.
		if subs, _ := statusfile.ListSubagents(repo.WorktreePath); len(subs) > 0 {
			s = statusfile.Subagents
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

// subagentIndent prefixes the single line of subagent emoji RenderRows (and
// Ls's no-fzf fallback) render directly beneath a session's own row.
const subagentIndent = "    "

// SubagentLine renders the single tabbed-in line listing one emoji per live
// subagent tracked under any of repos' worktree paths, in no particular
// cross-repo order. found is false when there are no live subagents at
// all, so callers can skip the line entirely rather than render a bare
// indent.
func SubagentLine(repos []state.Repo) (line string, found bool) {
	var emojis []string
	for _, repo := range repos {
		subs, err := statusfile.ListSubagents(repo.WorktreePath)
		if err != nil {
			continue
		}
		for _, sub := range subs {
			emojis = append(emojis, statusEmoji[sub.Status])
		}
	}
	if len(emojis) == 0 {
		return "", false
	}
	return subagentIndent + strings.Join(emojis, " "), true
}

// FormatLabel renders a session's row label:
// "<emoji> <name> | <Mon date> (<repos>) — <description>" (no
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

// RenderRows renders one "name\tlabel\tdesc" row per session in st, sorted
// most- to least-recently-opened via state.SortedSessions. When a session
// has live subagents,
// SubagentLine's emoji line is appended to label with an embedded "\n"
// rather than becoming a separate row: fzf's multi-line item support
// (see fzfutil.Run) renders that as a second visual line of the SAME
// item, so it can never be independently arrow-keyed onto.
func RenderRows(st *state.State) []string {
	return renderRows(st, true)
}

// renderRows is RenderRows' shared implementation. includeSubagents is
// false only for mt rm's picker (see rm.go): it has no way to act on a
// session's subagents, so showing them there would only be noise.
func renderRows(st *state.State, includeSubagents bool) []string {
	sessions := st.SortedSessions()
	rows := make([]string, 0, len(sessions))

	for _, sess := range sessions {
		status, found := SessionStatus(sess.Repos)
		label := FormatLabel(sess, status, found)

		if includeSubagents {
			if line, ok := SubagentLine(sess.Repos); ok {
				label += "\n" + line
			}
		}

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
// recreates) whichever session gets picked.
//
// cwd is accepted for interface symmetry with Create/PR/Spawn's
// entrypoints; Ls's own behavior never depends on the invoking directory,
// so it isn't used here.
func Ls(d *Deps, mtBinaryPath, cwd string, insideTmux bool) error {
	if len(d.State.SortedSessions()) == 0 {
		fmt.Fprintln(os.Stderr, "mt: no sessions tracked yet")
		return nil
	}

	if _, err := fzfLookPath("fzf"); err != nil {
		for _, sess := range d.State.SortedSessions() {
			status, found := SessionStatus(sess.Repos)
			fmt.Println(FormatLabel(sess, status, found))
			if line, ok := SubagentLine(sess.Repos); ok {
				fmt.Println(line)
			}
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
	var waitForPoller func()
	if listen {
		waitForPoller = fzfutil.StartReloadPoller(ctx, mtBinaryPath, socketPath)
	}

	// cancel (and wait) is called explicitly right here, immediately after
	// Run returns — not deferred to Ls's own return — so the poller's
	// lifetime stays scoped tightly around the blocking Run call and
	// never overlaps AttachOrRecreate's (possibly multi-call) rebuild of
	// a dead session. The poller no longer touches runner at all (it
	// talks to fzf's --listen socket directly over HTTP), so there's
	// nothing left here to race against AttachOrRecreate's tmux calls —
	// waiting is just a hard guarantee the goroutine has actually exited
	// before moving on, not a correctness requirement the way it was
	// when the poller shelled out through the same runner.
	selectedName, runErr := fzfutil.Run(runner, rows, args)
	cancel()
	if waitForPoller != nil {
		waitForPoller()
	}
	if runErr != nil {
		return runErr
	}
	if selectedName == "" {
		return nil
	}

	return AttachOrRecreate(d, selectedName, insideTmux, time.Now().Unix())
}

// newFzfSocketPath generates a unique, not-yet-existing path for fzf's
// --listen unix socket — a name, not a file: fzf creates/binds the socket
// itself, so the path must not already exist when it starts. A
// package-level var, like fzfLookPath above, so a test
// that needs StartReloadPoller's real HTTP-over-Unix-socket requests to
// land somewhere it's actually listening can override it to a known path
// instead of this process-generated one.
var newFzfSocketPath = defaultNewFzfSocketPath

func defaultNewFzfSocketPath() (string, error) {
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
// repo whose worktree directory is gone from tracking as it goes.
//
// now is stamped onto the session's LastOpened in BOTH branches, so that
// picking a session out of `mt ls` actually floats it to the top of the
// most-recently-opened ordering next time.
func AttachOrRecreate(d *Deps, name string, insideTmux bool, now int64) error {
	sessionID, err := d.Tmux.FindSessionID(name)
	if err != nil {
		return err
	}
	if sessionID != "" {
		d.State.UpsertSession(name, "", now)
		return saveAndAttach(d, sessionID, insideTmux)
	}

	var rebuiltSessionID string
	// ReposFor hands back a copy, which this loop depends on: RemoveRepo
	// below rewrites the session's own Repos slice in place, so ranging
	// over the live slice would shift the iteration out from under itself
	// and silently skip the repo after each pruned one.
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

	d.State.UpsertSession(name, "", now)
	return saveAndAttach(d, rebuiltSessionID, insideTmux)
}
