// Package fzfutil wraps the fzf subprocess invocation used by `mt ls`'s
// session picker, including its optional live-reload mechanism (ported
// from the original bash motree.sh's do_ls — see
// docs/superpowers/specs/2026-11-14-motree-go-design.md).
package fzfutil

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mauricebutts/motree/internal/executil"
	"github.com/mauricebutts/motree/internal/tmux"
)

// SupportsListen reports whether this machine's fzf can run `mt ls`'s
// live-reload mechanism: the installed fzf's --help output must mention
// --listen. The original bash do_ls additionally required `curl` on PATH
// (it shelled out to curl to talk to fzf's --listen socket); this port
// talks to that socket directly over HTTP instead (see
// StartReloadPoller), so no external HTTP client binary is required.
func SupportsListen(runner executil.Runner) bool {
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
// If fzf exits non-zero for any reason — its "no selection" status (exit
// code 1, from Esc/cancel), a signal like SIGINT from Ctrl-C (exit code
// 130), or anything else — Run returns ("", nil) rather than an error.
// This matches the original bash do_ls's `... fzf ...)" || return 0`,
// which treats every nonzero fzf exit identically: there is no bash-side
// distinction by exit code, so the Go port doesn't make one either.
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
		return "", nil
	}

	line := strings.TrimRight(stdout, "\n")
	if line == "" {
		return "", nil
	}

	name, _, _ := strings.Cut(line, "\t")
	return name, nil
}

// StartReloadPoller starts a background goroutine that, once per second,
// asks the fzf instance listening on socketPath to reload its rows by
// running `mt __mt_rows` again — porting the original bash do_ls's
// `( while :; do sleep 1; curl ...; done ) &` background poller. Where
// the original (and an earlier version of this port) shelled out to curl,
// this dials socketPath directly as an HTTP client over a Unix socket:
// fzf's --listen protocol is just a bare POST of the action string, which
// doesn't need an external binary to speak. Because of that, this no
// longer touches the executil.Runner at all, so — unlike when it shelled
// out through the same Runner callers also use for tmux/fzf/etc — there
// is nothing here left to race with whatever the caller does next.
//
// The goroutine runs until ctx is cancelled; StartReloadPoller itself
// returns immediately. Cancelling ctx both stops future ticks and (via
// the per-request context derived from it) aborts a request already in
// flight; the returned wait func blocks until the goroutine has actually
// exited, for callers that want a hard guarantee before moving on (e.g.
// AttachOrRecreate's tmux calls) rather than relying on cancellation
// alone.
func StartReloadPoller(ctx context.Context, mtBinaryPath, socketPath string) (wait func()) {
	done := make(chan struct{})

	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
	}

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
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/", strings.NewReader(reloadData))
				if err != nil {
					continue
				}
				if resp, err := client.Do(req); err == nil {
					resp.Body.Close()
				}
			}
		}
	}()

	return func() { <-done }
}
