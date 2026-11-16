package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mauricebutts/motree/internal/executil"
	"github.com/mauricebutts/motree/internal/gitutil"
	"github.com/mauricebutts/motree/internal/hook"
	"github.com/mauricebutts/motree/internal/hookinstall"
	"github.com/mauricebutts/motree/internal/mtcmd"
	"github.com/mauricebutts/motree/internal/state"
	"github.com/mauricebutts/motree/internal/tmux"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: mt [command] [args...]\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "__hook":
		// Hook command always exits 0, never surfaces errors to Claude Code
		if err := hook.Run(os.Stdin); err != nil {
			fmt.Fprintf(os.Stderr, "hook error: %v\n", err)
		}
		os.Exit(0)
	case "install-hooks":
		if err := runInstallHooks(); err != nil {
			fmt.Fprintf(os.Stderr, "install-hooks error: %v\n", err)
			os.Exit(1)
		}
	case "__mt_rows":
		if err := runRows(); err != nil {
			fmt.Fprintf(os.Stderr, "mt: %v\n", err)
			os.Exit(1)
		}
	case "__mt_preview":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: mt __mt_preview <name>\n")
			os.Exit(1)
		}
		if err := runPreview(os.Args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "mt: %v\n", err)
			os.Exit(1)
		}
	}
}

// newDeps loads the shared session state and wires up real git/tmux
// subprocess runners, for use by the subcommands below.
func newDeps() (*mtcmd.Deps, error) {
	st, err := state.Load(state.StateFilePath())
	if err != nil {
		return nil, err
	}
	return &mtcmd.Deps{
		Git:   gitutil.Git{Runner: executil.RealRunner{}},
		Tmux:  tmux.Tmux{Runner: executil.RealRunner{}},
		State: st,
	}, nil
}

// runRows prints one "name\tlabel\tdesc" line per tracked session, sorted
// most- to least-recently-opened, for the fzf picker to consume.
func runRows() error {
	d, err := newDeps()
	if err != nil {
		return err
	}
	for _, row := range mtcmd.RenderRows(d.State) {
		fmt.Println(row)
	}
	return nil
}

// runPreview prints a one-shot preview snapshot (description, and — when a
// claude pane is found — a divider and its captured tail) for the named
// session, for the fzf picker's preview window to consume.
func runPreview(name string) error {
	d, err := newDeps()
	if err != nil {
		return err
	}
	fmt.Println(mtcmd.Preview(d, name))
	return nil
}

// runInstallHooks registers mt's "__hook" subcommand with Claude Code by
// merging it into the user's ~/.claude/settings.json.
func runInstallHooks() error {
	mtBinaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve mt binary path: %w", err)
	}

	home := os.Getenv("HOME")
	if home == "" {
		return fmt.Errorf("HOME environment variable is not set")
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	if err := hookinstall.Merge(settingsPath, mtBinaryPath); err != nil {
		return err
	}

	fmt.Printf("Installed mt hooks into %s\n", settingsPath)
	return nil
}
