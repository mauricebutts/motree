package fzfutil

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mauricebutts/motree/internal/executil"
)

// shortSocketPath generates a short, not-yet-existing path for a test's
// Unix-socket listener, directly under the OS temp root rather than
// t.TempDir() (which nests under a directory named after the test
// function and routinely exceeds the ~104-byte sun_path limit on macOS).
// Matches the production defaultNewFzfSocketPath's own approach.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "mt-fzf-test-*.sock")
	if err != nil {
		t.Fatalf("os.CreateTemp: %v", err)
	}
	name := f.Name()
	f.Close()
	if err := os.Remove(name); err != nil {
		t.Fatalf("os.Remove(%q): %v", name, err)
	}
	t.Cleanup(func() { os.Remove(name) })
	return name
}

func TestBuildArgs_WithoutListen_OmitsListenFlag(t *testing.T) {
	args := BuildArgs("/usr/local/bin/mt", false, "/tmp/should-not-appear.sock")

	for _, a := range args {
		if strings.Contains(a, "--listen") {
			t.Errorf("args = %+v, want no --listen flag when listen=false", args)
		}
		if strings.Contains(a, "should-not-appear") {
			t.Errorf("args = %+v, socketPath leaked into args when listen=false", args)
		}
	}

	wantPreview := "--preview='/usr/local/bin/mt' __mt_preview {1}"
	if !contains(args, wantPreview) {
		t.Errorf("args = %+v, want to contain %q", args, wantPreview)
	}
	if !contains(args, "--preview-window=down,15,wrap,follow") {
		t.Errorf("args = %+v, want to contain --preview-window=down,15,wrap,follow", args)
	}
	if !contains(args, "--read0") || !contains(args, "--print0") {
		t.Errorf("args = %+v, want --read0 and --print0 (multi-line subagent rows need NUL framing)", args)
	}
}

func TestBuildArgs_WithListen_IncludesListenAndPreviewFlags(t *testing.T) {
	args := BuildArgs("/usr/local/bin/mt", true, "/tmp/mt-fzf-abc123.sock")

	if !contains(args, "--listen=/tmp/mt-fzf-abc123.sock") {
		t.Errorf("args = %+v, want to contain --listen=/tmp/mt-fzf-abc123.sock", args)
	}
	wantPreview := "--preview='/usr/local/bin/mt' __mt_preview {1}"
	if !contains(args, wantPreview) {
		t.Errorf("args = %+v, want to contain %q", args, wantPreview)
	}
}

func TestSupportsListen_FalseWhenFzfHelpLacksListen(t *testing.T) {
	runner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "usage: fzf [options]\n"},
		},
	}

	if got := SupportsListen(runner); got {
		t.Errorf("SupportsListen = true, want false (fzf --help has no --listen)")
	}
}

func TestSupportsListen_TrueWhenFzfHelpMentionsListen(t *testing.T) {
	runner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "    --listen=[addr]  Start HTTP server\n"},
		},
	}

	if got := SupportsListen(runner); !got {
		t.Errorf("SupportsListen = false, want true (fzf --help mentions --listen)")
	}

	wantCalls := []executil.Call{
		{Name: "fzf", Args: []string{"--help"}},
	}
	if len(runner.Calls) != 1 || runner.Calls[0].Name != wantCalls[0].Name {
		t.Errorf("runner Calls = %+v, want %+v", runner.Calls, wantCalls)
	}
}

func TestRun_FeedsRowsAsStdin_ReturnsSelectedName(t *testing.T) {
	rows := []string{"myfeature\tsome label\tsome desc", "other\tother label\tother desc"}
	args := []string{"--delimiter=\t", "--with-nth=2", "--read0", "--print0"}
	wantData := strings.Join(rows, "\x00") + "\x00"

	var capturedScript, capturedFileContents string
	var readErr error
	runner := &executil.FakeRunner{
		Responses: func(call executil.Call) (string, string, error) {
			if call.Name == "sh" && len(call.Args) == 2 {
				capturedScript = call.Args[1]

				// The temp rows file must still exist right now, while
				// Run's runner.Run call is still executing synchronously —
				// Run removes it via defer only after this call returns.
				idx := strings.LastIndex(capturedScript, "< ")
				if idx != -1 {
					tmpPath := strings.Trim(strings.TrimSpace(capturedScript[idx+2:]), "'")
					var data []byte
					data, readErr = os.ReadFile(tmpPath)
					capturedFileContents = string(data)
				}
			}
			return "myfeature\tsome label\tsome desc\x00", "", nil
		},
	}

	name, err := Run(runner, rows, args)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if name != "myfeature" {
		t.Errorf("Run selectedName = %q, want %q", name, "myfeature")
	}

	if capturedScript == "" {
		t.Fatalf("sh -c was never called")
	}
	if readErr != nil {
		t.Fatalf("reading temp rows file while Run's runner.Run call was executing: %v", readErr)
	}
	if capturedFileContents != wantData {
		t.Errorf("temp rows file contents = %q, want %q", capturedFileContents, wantData)
	}
}

// TestRun_RowWithEmbeddedNewline_StillExtractsName pins the reason rows
// are NUL- rather than newline-joined: a row's label can contain a literal
// "\n" (the subagent-status line RenderRows appends beneath a session's
// own label), and that must survive intact in the temp rows file fed to
// fzf, with name extraction from fzf's (NUL-terminated) selection still
// correctly stopping at the first tab rather than the embedded newline.
func TestRun_RowWithEmbeddedNewline_StillExtractsName(t *testing.T) {
	rows := []string{"myfeature\tsome label\nsubagent line\tsome desc"}
	args := []string{"--delimiter=\t", "--with-nth=2", "--read0", "--print0"}

	var capturedFileContents string
	runner := &executil.FakeRunner{
		Responses: func(call executil.Call) (string, string, error) {
			if call.Name == "sh" && len(call.Args) == 2 {
				script := call.Args[1]
				idx := strings.LastIndex(script, "< ")
				if idx != -1 {
					tmpPath := strings.Trim(strings.TrimSpace(script[idx+2:]), "'")
					data, _ := os.ReadFile(tmpPath)
					capturedFileContents = string(data)
				}
			}
			return "myfeature\tsome label\nsubagent line\tsome desc\x00", "", nil
		},
	}

	name, err := Run(runner, rows, args)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if name != "myfeature" {
		t.Errorf("Run selectedName = %q, want %q (must stop at the first tab, not the embedded newline)", name, "myfeature")
	}

	wantFileContents := rows[0] + "\x00"
	if capturedFileContents != wantFileContents {
		t.Errorf("temp rows file contents = %q, want %q — the embedded newline must survive NUL framing intact", capturedFileContents, wantFileContents)
	}
}

func TestRun_NoSelectionExitStatus1_ReturnsEmptyNoError(t *testing.T) {
	runner := &executil.FakeRunner{
		Responses: func(call executil.Call) (string, string, error) {
			return "", "", errors.New("exit status 1")
		},
	}

	name, err := Run(runner, []string{"a\tb\tc"}, []string{})
	if err != nil {
		t.Errorf("Run error = %v, want nil (exit status 1 means cancel, not a failure)", err)
	}
	if name != "" {
		t.Errorf("Run selectedName = %q, want empty", name)
	}
}

func TestRun_ExitStatus10_TreatedAsCancel(t *testing.T) {
	// Run doesn't discriminate by exit code — any nonzero fzf exit is a
	// graceful "no selection", not just status 1. This pins that exit
	// status 10 is one of them.
	runner := &executil.FakeRunner{
		Responses: func(call executil.Call) (string, string, error) {
			return "", "", errors.New("exit status 10")
		},
	}

	name, err := Run(runner, []string{"a\tb\tc"}, []string{})
	if err != nil {
		t.Errorf("Run error = %v, want nil (any nonzero fzf exit means cancel, not a failure)", err)
	}
	if name != "" {
		t.Errorf("Run selectedName = %q, want empty", name)
	}
}

func TestRun_ExitStatus130_SIGINTFromCtrlC_TreatedAsCancel(t *testing.T) {
	// Regression: Ctrl-C while the fzf picker is up sends SIGINT, which
	// exits with status 130 (128+SIGINT), not 1. This must be swallowed
	// the same as Esc; an earlier version special-cased only "exit status
	// 1" and surfaced this as a hard error instead.
	runner := &executil.FakeRunner{
		Responses: func(call executil.Call) (string, string, error) {
			return "", "", errors.New("exit status 130\nstderr: ")
		},
	}

	name, err := Run(runner, []string{"a\tb\tc"}, []string{})
	if err != nil {
		t.Errorf("Run error = %v, want nil (Ctrl-C/SIGINT means cancel, not a failure)", err)
	}
	if name != "" {
		t.Errorf("Run selectedName = %q, want empty", name)
	}
}

func TestRun_RealRunnerWrappedExitStatus1_ReturnsEmptyNoError(t *testing.T) {
	// RealRunner wraps the underlying exec error as "exit status 1\nstderr: ...".
	runner := &executil.FakeRunner{
		Responses: func(call executil.Call) (string, string, error) {
			return "", "", errors.New("exit status 1\nstderr: ")
		},
	}

	name, err := Run(runner, []string{"a\tb\tc"}, []string{})
	if err != nil {
		t.Errorf("Run error = %v, want nil", err)
	}
	if name != "" {
		t.Errorf("Run selectedName = %q, want empty", name)
	}
}

// recordedRequests is a mutex-guarded request log, used instead of a bare
// slice so a test can safely read it from the main goroutine while
// StartReloadPoller's background goroutine may still be concurrently
// appending to it via the HTTP handler goroutine(s) serving its requests.
type recordedRequests struct {
	mu     sync.Mutex
	bodies []string
}

func (r *recordedRequests) add(body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, body)
}

func (r *recordedRequests) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.bodies...)
}

func TestStartReloadPoller_PostsToSocketUntilCancelled(t *testing.T) {
	socketPath := shortSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen(unix, %q): %v", socketPath, err)
	}
	defer listener.Close()

	var recorded recordedRequests
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			recorded.add(string(body))
			w.WriteHeader(http.StatusOK)
		}),
	}
	go server.Serve(listener)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())

	wait := StartReloadPoller(ctx, "/usr/local/bin/mt", socketPath)

	time.Sleep(1200 * time.Millisecond)
	cancel()
	wait()
	requestsAfterCancel := len(recorded.snapshot())

	time.Sleep(1200 * time.Millisecond)
	if got := len(recorded.snapshot()); got != requestsAfterCancel {
		t.Errorf("recorded requests grew from %d to %d after cancel — poller did not stop", requestsAfterCancel, got)
	}
	if requestsAfterCancel == 0 {
		t.Fatalf("recorded requests = 0, want at least 1 reload request to have landed before cancel")
	}

	wantBody := "reload('/usr/local/bin/mt' __mt_rows)"
	if got := recorded.snapshot()[0]; got != wantBody {
		t.Errorf("request body = %q, want %q", got, wantBody)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
