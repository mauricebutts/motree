// Package fzfutil wraps the fzf subprocess invocation used by `mt ls`'s
// session picker, including its optional live-reload mechanism (ported
// from the original bash motree.sh's do_ls — see
// docs/superpowers/specs/2026-11-14-motree-go-design.md).
package fzfutil

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mauricebutts/motree/internal/executil"
	"github.com/mauricebutts/motree/internal/tmux"
)

// CurlLookPath resolves "curl" to an executable's path, exactly like
// exec.LookPath (its default value). It's a package-level var rather than
// a direct exec.LookPath call so SupportsListen is deterministically
// testable, the same trick ghutil.LookPath uses for "gh" — tests (in this
// package and in callers like mtcmd, which need to force listen=true to
// test Ls's live-reload path) can reassign it to force the "curl is/isn't
// on PATH" result, instead of SupportsListen's result silently depending
// on whether curl happens to be installed on whatever machine runs
// `go test`. Reassign the original value back (e.g. via t.Cleanup) after
// overriding it.
var CurlLookPath = exec.LookPath

// SupportsListen reports whether this machine's fzf can run `mt ls`'s
// live-reload mechanism: curl must be on PATH (the poller shells out to
// it), and the installed fzf's --help output must mention --listen. Both
// are required, mirroring the original bash do_ls's
// `command -v curl >/dev/null 2>&1 && fzf --help 2>&1 | grep -q -- '--listen'`
// check.
func SupportsListen(runner executil.Runner) bool {
	if _, err := CurlLookPath("curl"); err != nil {
		return false
	}

	stdout, stderr, _ := runner.Run("fzf", "--help")
	return strings.Contains(stdout, "--listen") || strings.Contains(stderr, "--listen")
}

// BuildArgs builds the fzf argv for `mt ls`'s picker, ported directly from
// the original bash do_ls's fzf invocation:
//
//	fzf --delimiter='\t' --with-nth=2 --height=~40% --reverse --track --id-nth=1 $listen_flag \
//	  --preview="'$script_path' __mt_preview {1}" --preview-window=down,30,wrap
//
// listen controls whether --listen=socketPath is included; socketPath is
// ignored when listen is false. mtBinaryPath is quoted (via tmux.Quote, the
// same POSIX single-quote escaping used throughout this codebase for
// values that must survive a receiving shell unexpanded) in place of the
// original's "$script_path".
func BuildArgs(mtBinaryPath string, listen bool, socketPath string) []string {
	args := []string{
		`--delimiter=\t`,
		"--with-nth=2",
		"--height=~40%",
		"--reverse",
		"--track",
		"--id-nth=1",
	}

	if listen {
		args = append(args, "--listen="+socketPath)
	}

	args = append(args,
		"--preview="+tmux.Quote(mtBinaryPath)+" __mt_preview {1}",
		"--preview-window=down,30,wrap",
	)

	return args
}

// Run runs fzf with args, feeding rows (newline-joined) as its stdin, and
// returns the selected session name (the first tab-delimited field of
// whatever fzf printed to stdout).
//
// executil.Runner.Run has no stdin parameter (its signature is shared
// verbatim by gitutil/tmux/ghutil, so it isn't fzfutil's to change) — so,
// same as ghutil.runIn's "cd <dir> && ..." trick for the cwd it likewise
// lacks, Run writes rows to a temp file and shells out via
// `sh -c "fzf <args> < <tmpfile>"`, each argument POSIX-single-quote-escaped
// (tmux.Quote) so it survives unexpanded regardless of spaces or shell
// metacharacters.
//
// If fzf exits with its "no selection" status (exit code 1, which
// cancel/Esc produces), Run returns ("", nil) rather than an error —
// matching the original bash do_ls's `... fzf ...)" || return 0`.
func Run(runner executil.Runner, rows []string, args []string) (selectedName string, err error) {
	tmpFile, err := os.CreateTemp("", "mt-fzf-rows-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(strings.Join(rows, "\n") + "\n"); err != nil {
		tmpFile.Close()
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		return "", err
	}

	parts := make([]string, 0, len(args)+1)
	parts = append(parts, tmux.Quote("fzf"))
	for _, a := range args {
		parts = append(parts, tmux.Quote(a))
	}
	script := strings.Join(parts, " ") + " < " + tmux.Quote(tmpPath)

	stdout, _, runErr := runner.Run("sh", "-c", script)
	if runErr != nil {
		if isExitStatus1(runErr) {
			return "", nil
		}
		return "", runErr
	}

	line := strings.TrimRight(stdout, "\n")
	if line == "" {
		return "", nil
	}

	name, _, _ := strings.Cut(line, "\t")
	return name, nil
}

// isExitStatus1 reports whether err's message indicates fzf exited with
// status 1 specifically (its "no selection" status, which Esc/cancel
// produces) — not merely containing "exit status 1" as a substring, which
// would also (wrongly) match exit statuses 10-19. RealRunner wraps the
// underlying *exec.ExitError's "exit status N" message as
// "exit status N\nstderr: ...", and tests exercise bare "exit status N"
// errors directly, so both forms are checked.
func isExitStatus1(err error) bool {
	msg := err.Error()
	return msg == "exit status 1" || strings.HasPrefix(msg, "exit status 1\n") || strings.HasPrefix(msg, "exit status 1 ")
}

// StartReloadPoller starts a background goroutine that, once per second,
// asks the fzf instance listening on socketPath to reload its rows by
// running `mt __mt_rows` again — porting the original bash do_ls's
// `( while :; do sleep 1; curl ...; done ) &` background poller. The
// goroutine runs until ctx is cancelled; StartReloadPoller itself returns
// immediately. Callers control the goroutine's lifetime precisely around
// their blocking Run call: call ctx's cancel func right after Run returns,
// then call the returned wait func, before doing anything else with
// runner (e.g. starting AttachOrRecreate's tmux calls). Cancelling ctx
// only stops *future* ticks — a curl call already in flight when cancel
// is called keeps running concurrently with whatever the caller does
// next, racing on runner, unless the caller also waits for wait() to
// return, which blocks until the goroutine (and any curl call it was
// mid-flight on) has fully exited.
func StartReloadPoller(ctx context.Context, runner executil.Runner, mtBinaryPath, socketPath string) (wait func()) {
	done := make(chan struct{})

	go func() {
		defer close(done)

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		reloadData := "reload(" + tmux.Quote(mtBinaryPath) + " __mt_rows)"

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _, _ = runner.Run("curl", "-s", "-o", "/dev/null", "--unix-socket", socketPath, "http", "-d", reloadData)
			}
		}
	}()

	return func() { <-done }
}
