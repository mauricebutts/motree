// Package mtcmd implements the user-facing `mt` subcommands by composing
// the gitutil, tmux, state, and claudecmd packages.
package mtcmd

import (
	"github.com/mauricebutts/motree/internal/gitutil"
	"github.com/mauricebutts/motree/internal/state"
	"github.com/mauricebutts/motree/internal/tmux"
)

// Deps bundles the subsystem clients that mtcmd commands operate against.
type Deps struct {
	Git   gitutil.Git
	Tmux  tmux.Tmux
	State *state.State
}

// saveAndAttach persists state to disk and only then hands the terminal
// over to tmux.
//
// The ordering is the whole point. Attaching (or switch-client'ing into a
// session whose pane is running this very process) takes the terminal over
// for the lifetime of that tmux session, and can tear this process down
// outright — so anything cmd/mt/main.go would have saved *after* the
// command returns may never be written. Every attach site in this package
// therefore goes through here instead of calling Tmux.AttachOrSwitch
// directly. main.go's own trailing Save is then just a harmless re-save
// that still covers the paths which never attach at all.
func saveAndAttach(d *Deps, sessionID string, insideTmux bool) error {
	if err := d.State.Save(state.StateFilePath()); err != nil {
		return err
	}
	return d.Tmux.AttachOrSwitch(sessionID, insideTmux)
}
