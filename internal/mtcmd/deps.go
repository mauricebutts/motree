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
