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
// they've already printed a usage message to stderr themselves — main's
// dispatch checks for it so it doesn't also print a redundant "mt: usage
// shown" line via its normal "mt: %v" error-printing path.
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
		if err := runRm(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "mt: %v\n", err)
			os.Exit(1)
		}
	case "cmd":
		if err := runCmd(); err != nil {
			fmt.Fprintf(os.Stderr, "mt: %v\n", err)
			os.Exit(1)
		}
	case "__mt_cmd_dock":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: mt __mt_cmd_dock <name>\n")
			os.Exit(1)
		}
		if err := runCmdDock(os.Args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "mt: %v\n", err)
			os.Exit(1)
		}
	case "__mt_cmd_list":
		if err := runCmdList(); err != nil {
			fmt.Fprintf(os.Stderr, "mt: %v\n", err)
			os.Exit(1)
		}
	case "__mt_cmd_quit":
		if err := runCmdQuit(); err != nil {
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
		// Not a recognized subcommand — anything else is treated as the
		// bare `mt <name> [-b <base-branch>] [-d <description>]` form, so
		// os.Args[1] here is the session name, not a typo'd subcommand.
		if err := runCreate(os.Args[1:]); err != nil {
			reportAndExit(err)
		}
	}
}

// reportAndExit prints err and exits 1, skipping the print for
// errUsageShown — the one sentinel meaning "the callee already printed
// this itself" (it wrote usage() to stderr). Shared by the
// create/spawn/pr dispatch branches above, the only ones that produce it.
func reportAndExit(err error) {
	if err != errUsageShown {
		fmt.Fprintf(os.Stderr, "mt: %v\n", err)
	}
	os.Exit(1)
}

// usage prints the one-line usage summary.
func usage() {
	fmt.Fprintln(os.Stderr, `Usage: mt <name> [-b <base-branch>] [-d <description>] | mt spawn <name> "<prompt>" [-b <base-branch>] [-d <description>] | mt pr <pr_number> | mt ls | mt rm [--self] | mt cmd`)
}

// printHelp prints mt's static help text for every subcommand.
func printHelp() {
	fmt.Print(`mt <name> [-b <base-branch>] [-d <description>]
    Create or expand a named session: a git worktree paired with a tmux
    session running nvim, a shell, and claude.

mt spawn <name> "<prompt>" [-b <base-branch>] [-d <description>]
    Like the above, but runs <prompt> in the claude pane immediately and
    does not attach — for firing off several sessions in a row.

mt pr <pr_number>
    Look up <pr_number> with gh. If gh can't resolve it, report gh's own
    error — which distinguishes a PR that doesn't exist from a repo gh
    can't see (wrong account, SSO not authorized). Otherwise create/attach
    a session named pr-<pr_number>, with a worktree checked out to that
    PR's branch, and have the claude pane open already explaining the PR.

mt ls
    List tracked sessions and attach to the one you pick.

mt rm
    List tracked sessions and remove the one you pick (worktree, branch,
    tmux session, and tracked state).

mt rm --self
    Remove the session you're currently attached to, after confirming.

mt cmd
    Open the command center: a live picker of tracked sessions on the
    left, and whichever one you pick's claude pane, live and interactive,
    on the right. Pick a different session to switch the right pane.
    Esc closes the picker but leaves the command center running (re-open
    with 'mt cmd'); Ctrl-C un-zooms whatever's docked and closes it for
    good.

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

// runRows prints one NUL-terminated "name\tlabel\tdesc" record per tracked
// session, sorted most- to least-recently-opened, for fzf's live-reload
// `reload(mt __mt_rows)` binding to consume. It must match the --read0
// framing fzf itself is launched with (see fzfutil.BuildArgs/Run) — a
// plain newline per row would merge every session into a single record
// the instant a reload fires, since a row's label can itself contain a
// literal "\n" (the subagent-status line) with no other record separator
// in the stream.
func runRows() error {
	d, err := newDeps()
	if err != nil {
		return err
	}
	for _, row := range mtcmd.RenderRows(d.State) {
		fmt.Print(row + "\x00")
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
// whichever session gets picked.
//
// There is deliberately no Save here. AttachOrRecreate persists its own
// LastOpened bump and repo pruning before it hands the terminal to tmux —
// it has to, since attaching blocks for the whole life of that tmux
// session and can tear this process down. Saving again afterwards would be
// worse than redundant: this process's in-memory State is a snapshot from
// before the attach, so writing it back on detach would clobber anything
// other `mt` invocations recorded in the meantime. The paths that return
// without attaching (no sessions tracked, fzf missing, picker cancelled)
// mutate no state, so they have nothing to persist.
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

	return mtcmd.Ls(d, mtBinaryPath, cwd, insideTmux)
}

// runRm runs `mt rm`: with "--self", removes the session the caller is
// currently attached to (after a y/N confirmation); otherwise runs the
// fzf session picker, removing whichever session gets picked (its
// worktrees, branches, state, and tmux session). args is os.Args[2:]
// (everything after "rm").
//
// As with runLs, no Save here: RemoveSession writes the pruned state out
// itself, before the kill-session that can SIGHUP this very process.
func runRm(args []string) error {
	d, err := newDeps()
	if err != nil {
		return err
	}

	if len(args) > 0 && args[0] == "--self" {
		return mtcmd.RmSelf(d, os.Stdin, os.Stdout)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	return mtcmd.Rm(d, cwd)
}

// runCmd runs `mt cmd`: finds or creates the command center and attaches
// to it.
func runCmd() error {
	d, err := newDeps()
	if err != nil {
		return err
	}

	mtBinaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve mt binary path: %w", err)
	}

	insideTmux := os.Getenv("TMUX") != ""

	return mtcmd.Cmd(d, mtBinaryPath, insideTmux)
}

// runCmdDock runs `mt __mt_cmd_dock <name>`: makes the command center's
// right pane mirror name's claude pane.
func runCmdDock(name string) error {
	d, err := newDeps()
	if err != nil {
		return err
	}

	return mtcmd.Dock(d, name)
}

// runCmdList runs `mt __mt_cmd_list`: the command center's left-pane fzf
// picker, whose own bindings drive runCmdDock/runCmdQuit via execute-silent.
func runCmdList() error {
	d, err := newDeps()
	if err != nil {
		return err
	}

	mtBinaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve mt binary path: %w", err)
	}

	return mtcmd.List(d, mtBinaryPath)
}

// runCmdQuit runs `mt __mt_cmd_quit`: tears down the command center.
func runCmdQuit() error {
	d, err := newDeps()
	if err != nil {
		return err
	}

	return mtcmd.Quit(d)
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

	// Create persists state itself, right before it attaches (see
	// mtcmd.saveAndAttach) — so there's no Save here. Attaching blocks for
	// the life of the tmux session, so a Save after it returned would both
	// come far too late and risk overwriting what other `mt` invocations
	// wrote while the user was attached.
	return mtcmd.Create(d, name, *baseBranch, *description, cwd, insideTmux, now)
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
	if err := d.State.Save(state.StateFilePath()); err != nil {
		return err
	}

	// Unlike Create/PR, Spawn never attaches — so without this, a
	// successful spawn gives the user no feedback at all.
	fmt.Printf("mt: spawned session '%s'\n", name)
	return nil
}

// runPR implements `mt pr <pr_number>` — no flags, just the positional PR
// number. args is os.Args[2:] (everything after "pr").
func runPR(args []string) error {
	if len(args) == 0 {
		usage()
		return errUsageShown
	}
	prNumber := args[0]
	title := ""

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

	// Same as runCreate: PR saves before it attaches, so nothing to do here.
	return mtcmd.PR(d, gh, prNumber, title, cwd, insideTmux, now)
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
