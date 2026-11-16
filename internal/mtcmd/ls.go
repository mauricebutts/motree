package mtcmd

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/mauricebutts/motree/internal/state"
	"github.com/mauricebutts/motree/internal/statusfile"
)

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
