package fzfutil

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mauricebutts/motree/internal/executil"
)

// forceCurlUnavailable overrides curlLookPath so SupportsListen's curl
// check deterministically fails, independent of whether curl actually
// happens to be installed on whatever machine runs `go test`.
func forceCurlUnavailable(t *testing.T) {
	t.Helper()
	orig := curlLookPath
	curlLookPath = func(file string) (string, error) { return "", errors.New("executable file not found in $PATH") }
	t.Cleanup(func() { curlLookPath = orig })
}

func forceCurlAvailable(t *testing.T) {
	t.Helper()
	orig := curlLookPath
	curlLookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	t.Cleanup(func() { curlLookPath = orig })
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
	if !contains(args, "--preview-window=down,30,wrap") {
		t.Errorf("args = %+v, want to contain --preview-window=down,30,wrap", args)
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

func TestSupportsListen_FalseWhenCurlMissing(t *testing.T) {
	forceCurlUnavailable(t)

	runner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "    --listen=[addr]  Start HTTP server\n"},
		},
	}

	if got := SupportsListen(runner); got {
		t.Errorf("SupportsListen = true, want false (curl missing)")
	}
	if len(runner.Calls) != 0 {
		t.Errorf("runner Calls = %+v, want none — curl check must short-circuit before running fzf --help", runner.Calls)
	}
}

func TestSupportsListen_FalseWhenFzfHelpLacksListen(t *testing.T) {
	forceCurlAvailable(t)

	runner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "usage: fzf [options]\n"},
		},
	}

	if got := SupportsListen(runner); got {
		t.Errorf("SupportsListen = true, want false (fzf --help has no --listen)")
	}
}

func TestSupportsListen_TrueWhenCurlPresentAndFzfHelpMentionsListen(t *testing.T) {
	forceCurlAvailable(t)

	runner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "    --listen=[addr]  Start HTTP server\n"},
		},
	}

	if got := SupportsListen(runner); !got {
		t.Errorf("SupportsListen = false, want true (curl present, fzf --help mentions --listen)")
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
	args := []string{"--delimiter=\t", "--with-nth=2"}
	wantData := strings.Join(rows, "\n") + "\n"

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
			return "myfeature\tsome label\tsome desc\n", "", nil
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

func TestRun_OtherNonZeroExit_ReturnsError(t *testing.T) {
	runner := &executil.FakeRunner{
		Responses: func(call executil.Call) (string, string, error) {
			return "", "", errors.New("exit status 2")
		},
	}

	_, err := Run(runner, []string{"a\tb\tc"}, []string{})
	if err == nil {
		t.Errorf("Run error = nil, want non-nil for a non-cancel failure")
	}
}

func TestRun_ExitStatus10_ReturnsError_NotMistakenForExitStatus1(t *testing.T) {
	// Regression: a naive strings.Contains(msg, "exit status 1") check
	// would wrongly treat "exit status 10" as the exit-1 cancel case.
	runner := &executil.FakeRunner{
		Responses: func(call executil.Call) (string, string, error) {
			return "", "", errors.New("exit status 10")
		},
	}

	_, err := Run(runner, []string{"a\tb\tc"}, []string{})
	if err == nil {
		t.Errorf("Run error = nil, want non-nil — exit status 10 is not the exit-1 cancel case")
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

// recordedCalls is a mutex-guarded call log, used instead of
// executil.FakeRunner's own (unguarded) Calls slice so a test can safely
// read it from the main goroutine while StartReloadPoller's background
// goroutine may still be concurrently appending to it.
type recordedCalls struct {
	mu    sync.Mutex
	calls []executil.Call
}

func (r *recordedCalls) add(c executil.Call) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
}

func (r *recordedCalls) snapshot() []executil.Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]executil.Call{}, r.calls...)
}

func TestStartReloadPoller_PostsToSocketUntilCancelled(t *testing.T) {
	var recorded recordedCalls
	runner := &executil.FakeRunner{
		Responses: func(call executil.Call) (string, string, error) {
			recorded.add(call)
			return "", "", nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())

	StartReloadPoller(ctx, runner, "/usr/local/bin/mt", "/tmp/mt-fzf-test.sock")

	time.Sleep(1200 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)
	callsAfterCancel := len(recorded.snapshot())

	time.Sleep(1200 * time.Millisecond)
	if got := len(recorded.snapshot()); got != callsAfterCancel {
		t.Errorf("recorded calls grew from %d to %d after cancel — poller did not stop", callsAfterCancel, got)
	}
	if callsAfterCancel == 0 {
		t.Fatalf("recorded calls = 0, want at least 1 curl call to have fired before cancel")
	}

	call := recorded.snapshot()[0]
	if call.Name != "curl" {
		t.Errorf("call.Name = %q, want %q", call.Name, "curl")
	}
	wantArgs := []string{"-s", "-o", "/dev/null", "--unix-socket", "/tmp/mt-fzf-test.sock", "http", "-d", "reload('/usr/local/bin/mt' __mt_rows)"}
	if strings.Join(call.Args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("call.Args = %+v, want %+v", call.Args, wantArgs)
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
