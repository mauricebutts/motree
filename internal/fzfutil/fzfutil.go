// Package fzfutil wraps the fzf subprocess invocation used by `mt ls`'s
// session picker, including its optional live-reload mechanism (see
// docs/superpowers/specs/2026-11-14-motree-go-design.md).
package fzfutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mauricebutts/motree/internal/executil"
	"github.com/mauricebutts/motree/internal/tmux"
)

// lsPreviewHeightPercent is the percentage of mt ls's fzf window height its
// live preview pane occupies (fzf's --preview-window=down,<N>,wrap) — the
// rest goes to the row list above it.
const lsPreviewHeightPercent = 25

// SupportsListen reports whether this machine's fzf can run `mt ls`'s
// live-reload mechanism: the installed fzf's --help output must mention
// --listen. StartReloadPoller talks to that socket directly over HTTP, so
// no external HTTP client binary (e.g. curl) is required.
func SupportsListen(runner executil.Runner) bool {
	stdout, stderr, _ := runner.Run("fzf", "--help")
	return strings.Contains(stdout, "--listen") || strings.Contains(stderr, "--listen")
}

// BuildArgs builds the fzf argv for `mt ls`'s picker:
//
//	fzf --delimiter='\t' --with-nth=2 --height=100% --reverse --track --id-nth=1 --read0 --print0 $listen_flag \
//	  --preview="'<mt binary>' __mt_preview {1}" --preview-window=down,<lsPreviewHeightPercent>,wrap,follow
//
// --height=100% (matching `mt cmd`'s own picker) rather than a smaller
// fraction: lsPreviewHeightPercent is itself a percentage OF this height,
// so capping the overall UI short also caps how much room the preview
// pane can ever get, leaving dead terminal space below the whole picker
// instead of giving that room to either the row list or the preview.
//
// --read0/--print0 are needed for multi-line rows — see Run. follow keeps
// the preview scrolled to its bottom (tail -f style) rather than its top,
// which matters because Preview's captured pane snapshot is a fixed number
// of lines regardless of how tall the preview pane actually renders —
// without follow, a short preview pane would clip the newest lines at the
// bottom instead of the oldest ones at the top.
//
// listen controls whether --listen=socketPath is included; socketPath is
// ignored when listen is false. mtBinaryPath is quoted (via tmux.Quote, the
// same POSIX single-quote escaping used throughout this codebase for
// values that must survive a receiving shell unexpanded).
func BuildArgs(mtBinaryPath string, listen bool, socketPath string) []string {
	args := []string{
		`--delimiter=\t`,
		"--with-nth=2",
		"--height=100%",
		"--reverse",
		"--track",
		"--id-nth=1",
		"--read0",
		"--print0",
	}

	if listen {
		args = append(args, "--listen="+socketPath)
	}

	args = append(args,
		"--preview="+tmux.Quote(mtBinaryPath)+" __mt_preview {1}",
		fmt.Sprintf("--preview-window=down,%d,wrap,follow", lsPreviewHeightPercent),
	)

	return args
}

// Run runs fzf with args, feeding rows (NUL-joined) as its stdin, and
// returns the selected session name (the first tab-delimited field of
// whatever fzf printed to stdout).
//
// Rows are NUL- rather than newline-joined, and args is expected to include
// --read0/--print0 (BuildArgs and cmdListFzfArgs both do), because a row's
// label can itself contain a literal "\n" — the subagent-status line
// RenderRows appends beneath a session's own label. A plain newline-joined
// feed would split that onto its own (independently selectable) fzf item;
// fzf's multi-line item support (0.53+, see
// https://junegunn.github.io/fzf/releases/0.53.0/) instead renders it as a
// second visual line of the SAME item, so arrow-key navigation can never
// land on it directly. This requires fzf >= 0.53.0 (see README) — there's
// no fallback for older fzf.
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
// 130), or anything else — Run returns ("", nil) rather than an error. It
// treats every nonzero fzf exit identically: there is no distinction by
// exit code.
func Run(runner executil.Runner, rows []string, args []string) (selectedName string, err error) {
	tmpFile, err := os.CreateTemp("", "mt-fzf-rows-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(strings.Join(rows, "\x00") + "\x00"); err != nil {
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

	line := strings.TrimRight(stdout, "\x00")
	if line == "" {
		return "", nil
	}

	name, _, _ := strings.Cut(line, "\t")
	return name, nil
}

// StartReloadPoller starts a background goroutine that, once per second,
// asks the fzf instance listening on socketPath to reload its rows by
// running `mt __mt_rows` again. It dials socketPath directly as an HTTP
// client over a Unix socket: fzf's --listen protocol is just a bare POST
// of the action string, which doesn't need an external binary to speak.
// Because of that, this never touches the executil.Runner at all, so —
// unlike something that shelled out through the same Runner callers also
// use for tmux/fzf/etc — there is nothing here left to race with whatever
// the caller does next.
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
