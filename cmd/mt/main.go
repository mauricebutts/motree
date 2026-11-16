package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mauricebutts/motree/internal/executil"
	"github.com/mauricebutts/motree/internal/ghutil"
	"github.com/mauricebutts/motree/internal/gitutil"
	"github.com/mauricebutts/motree/internal/hook"
	"github.com/mauricebutts/motree/internal/hookinstall"
	"github.com/mauricebutts/motree/internal/mtcmd"
	"github.com/mauricebutts/motree/internal/state"
	"github.com/mauricebutts/motree/internal/tmux"
)

// errUsageShown is a sentinel returned by runCreate/runSpawn/runPR when
// they've already printed a usage message to stderr themselves (mirroring
// the original bash tool's usage()-then-exit-1 pattern) — main's dispatch
// checks for it so it doesn't also print a redundant "mt: usage shown"
// line via its normal "mt: %v" error-printing path.
var errUsageShown = errors.New("usage shown")

func main() {
	if len(os.Args) < 2 {
		usage()
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
	case "ls":
		if err := runLs(); err != nil {
			fmt.Fprintf(os.Stderr, "mt: %v\n", err)
			os.Exit(1)
		}
	case "rm":
		if err := runRm(); err != nil {
			fmt.Fprintf(os.Stderr, "mt: %v\n", err)
			os.Exit(1)
		}
	case "spawn":
		if err := runSpawn(os.Args[2:]); err != nil {
			reportAndExit(err)
		}
	case "pr":
		if err := runPR(os.Args[2:]); err != nil {
			reportAndExit(err)
		}
	case "help", "-h", "--help":
		printHelp()
	default:
		// Not a recognized subcommand — the original bash tool's own
		// dispatch (a case statement over the same fixed list above, falling
		// through to its bottom "main" flow) treats anything else as the
		// bare `mt <name> [-b <base-branch>] [-d <description>]` form, so
		// os.Args[1] here is the session name, not a typo'd subcommand.
		if err := runCreate(os.Args[1:]); err != nil {
			reportAndExit(err)
		}
	}
}

// reportAndExit prints err (unless it's errUsageShown, meaning the callee
// already printed its own usage message to stderr) and exits 1. Shared by
// the create/spawn/pr dispatch branches above, the only ones that can
// produce errUsageShown.
func reportAndExit(err error) {
	if err != errUsageShown {
		fmt.Fprintf(os.Stderr, "mt: %v\n", err)
	}
	os.Exit(1)
}

// usage prints the one-line usage summary, matching the original bash
// tool's usage() (ported to the "mt" binary name; the Go port never
// implemented "rm --self", so that original's trailing "[--self]" is
// dropped here).
func usage() {
	fmt.Fprintln(os.Stderr, `Usage: mt <name> [-b <base-branch>] [-d <description>] | mt spawn <name> "<prompt>" [-b <base-branch>] [-d <description>] | mt pr <pr_number> | mt ls | mt rm`)
}

// printHelp prints mt's static help text, matching the original bash
// tool's do_help verbatim for every subcommand it still has (dropping its
// unimplemented "rm --self" section), plus a section for install-hooks,
// which only exists in this Go port.
func printHelp() {
	fmt.Print(`mt <name> [-b <base-branch>] [-d <description>]
    Create or expand a named session: a git worktree paired with a tmux
    session running nvim, a shell, and claude.

mt spawn <name> "<prompt>" [-b <base-branch>] [-d <description>]
    Like the above, but runs <prompt> in the claude pane immediately and
    does not attach — for firing off several sessions in a row.

mt pr <pr_number>
    Look up <pr_number> with gh. If it doesn't exist, print "no pr found".
    Otherwise create/attach a session named pr-<pr_number>, with a worktree
    checked out to that PR's branch, and have the claude pane open already
    explaining the PR.

mt ls
    List tracked sessions and attach to the one you pick.

mt rm
    List tracked sessions and remove the one you pick (worktree, branch,
    tmux session, and tracked state).

mt install-hooks
    One-time setup: register mt's Claude Code status hook in
    ~/.claude/settings.json (additive — existing hook entries are left
    untouched).
`)
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

// runLs runs the `mt ls` fzf session picker, attaching to (or recreating)
// whichever session gets picked, then persists whatever state Ls touched
// (AttachOrRecreate's own LastOpened bump, and any repo rows it pruned for
// worktrees that had gone missing).
func runLs() error {
	d, err := newDeps()
	if err != nil {
		return err
	}

	mtBinaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve mt binary path: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	insideTmux := os.Getenv("TMUX") != ""

	if err := mtcmd.Ls(d, mtBinaryPath, cwd, insideTmux); err != nil {
		return err
	}
	return d.State.Save(state.StateFilePath())
}

// runRm runs the `mt rm` fzf session picker, removing whichever session
// gets picked (its worktrees, branches, tmux session, and state), then
// persists the resulting state.
func runRm() error {
	d, err := newDeps()
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	if err := mtcmd.Rm(d, cwd); err != nil {
		return err
	}
	return d.State.Save(state.StateFilePath())
}

// runCreate implements the bare `mt <name> [-b <base-branch>] [-d
// <description>]` form: create or expand a named session. args is
// os.Args[1:] (args[0] is the session name).
func runCreate(args []string) error {
	if len(args) == 0 {
		usage()
		return errUsageShown
	}
	name := args[0]

	fs := flag.NewFlagSet("mt", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	baseBranch := fs.String("b", "", "base branch")
	description := fs.String("d", "", "description")
	if err := fs.Parse(args[1:]); err != nil {
		usage()
		return errUsageShown
	}

	d, err := newDeps()
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	insideTmux := os.Getenv("TMUX") != ""
	now := time.Now().Unix()

	if err := mtcmd.Create(d, name, *baseBranch, *description, cwd, insideTmux, now); err != nil {
		return err
	}
	return d.State.Save(state.StateFilePath())
}

// runSpawn implements `mt spawn <name> "<prompt>" [-b <base-branch>] [-d
// <description>]`: spawn a brand-new session running prompt immediately,
// without attaching. args is os.Args[2:] (everything after "spawn").
func runSpawn(args []string) error {
	if len(args) == 0 {
		usage()
		return errUsageShown
	}
	name := args[0]

	rest := args[1:]
	if len(rest) == 0 {
		usage()
		return errUsageShown
	}
	prompt := rest[0]
	if strings.HasPrefix(prompt, "-") {
		fmt.Fprintln(os.Stderr, "mt: <prompt> must come before any flags")
		usage()
		return errUsageShown
	}

	fs := flag.NewFlagSet("mt spawn", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	baseBranch := fs.String("b", "", "base branch")
	description := fs.String("d", "", "description")
	if err := fs.Parse(rest[1:]); err != nil {
		usage()
		return errUsageShown
	}

	d, err := newDeps()
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	now := time.Now().Unix()

	if err := mtcmd.Spawn(d, name, prompt, *baseBranch, *description, cwd, now); err != nil {
		return err
	}
	return d.State.Save(state.StateFilePath())
}

// runPR implements `mt pr <pr_number>` — no flags, just the positional PR
// number. args is os.Args[2:] (everything after "pr").
func runPR(args []string) error {
	if len(args) == 0 {
		usage()
		return errUsageShown
	}
	prNumber := args[0]

	d, err := newDeps()
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	insideTmux := os.Getenv("TMUX") != ""
	now := time.Now().Unix()
	gh := ghutil.GH{Runner: executil.RealRunner{}}

	if err := mtcmd.PR(d, gh, prNumber, cwd, insideTmux, now); err != nil {
		return err
	}
	return d.State.Save(state.StateFilePath())
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
