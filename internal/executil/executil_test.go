package executil

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRealRunner_SatisfiesRunnerInterface(t *testing.T) {
	// Verify that RealRunner{} (bare value) satisfies the Runner interface,
	// as expected by the task brief
	var _ Runner = RealRunner{}
}

func TestRealRunner_Run_CapturesOutputAndExitError(t *testing.T) {
	r := RealRunner{}
	stdout, stderr, err := r.Run("sh", "-c", "echo out; echo err >&2; exit 3")

	// assert stdout == "out\n"
	if stdout != "out\n" {
		t.Errorf("stdout = %q, want %q", stdout, "out\n")
	}

	// assert stderr == "err\n"
	if stderr != "err\n" {
		t.Errorf("stderr = %q, want %q", stderr, "err\n")
	}

	// assert err != nil and includes "exit status 3"
	if err == nil {
		t.Errorf("err = nil, want non-nil error with exit status 3")
	}

	if err != nil && !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("err.Error() = %q, want to contain 'exit status 3'", err.Error())
	}
}

// TestRealRunner_RunInteractive_InheritsParentStdio proves the escape hatch
// "tmux attach-session" depends on actually hands the child this process's
// own stdin/stdout/stderr, rather than Run's nil stdin and bytes.Buffer
// captures. The child writes what it sees on each descriptor to a file, so
// the assertion doesn't depend on having a real terminal — a unit test
// can't give it one, and whether tmux is happy with the resulting tty is
// only verifiable by running the built binary under a real terminal.
func TestRealRunner_RunInteractive_InheritsParentStdio(t *testing.T) {
	dir := t.TempDir()
	stdinPath := filepath.Join(dir, "in")
	outPath := filepath.Join(dir, "out")
	errPath := filepath.Join(dir, "err")

	if err := os.WriteFile(stdinPath, []byte("from-parent-stdin\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	stdin, err := os.Open(stdinPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer stdin.Close()
	stdout, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer stdout.Close()
	stderrFile, err := os.Create(errPath)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer stderrFile.Close()

	origIn, origOut, origErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = stdin, stdout, stderrFile
	runErr := RealRunner{}.RunInteractive("sh", "-c", "cat; echo to-stderr >&2")
	os.Stdin, os.Stdout, os.Stderr = origIn, origOut, origErr

	if runErr != nil {
		t.Fatalf("RunInteractive returned error: %v", runErr)
	}

	gotOut, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile out: %v", err)
	}
	if string(gotOut) != "from-parent-stdin\n" {
		t.Errorf("child stdout = %q, want %q — stdin and stdout must both be the parent's", gotOut, "from-parent-stdin\n")
	}

	gotErr, err := os.ReadFile(errPath)
	if err != nil {
		t.Fatalf("ReadFile err: %v", err)
	}
	if string(gotErr) != "to-stderr\n" {
		t.Errorf("child stderr = %q, want %q", gotErr, "to-stderr\n")
	}
}

func TestRealRunner_RunInteractive_PropagatesExitError(t *testing.T) {
	if err := (RealRunner{}).RunInteractive("sh", "-c", "exit 7"); err == nil || !strings.Contains(err.Error(), "exit status 7") {
		t.Errorf("RunInteractive error = %v, want one containing 'exit status 7'", err)
	}
}

// TestFakeRunner_RunInteractive_RecordsFlaggedCall keeps the test double
// honest: argv assertions elsewhere rely on Interactive distinguishing the
// two Runner methods, and on both drawing from the same response queue.
func TestFakeRunner_RunInteractive_RecordsFlaggedCall(t *testing.T) {
	var _ Runner = &FakeRunner{}

	f := &FakeRunner{Queue: []Response{{Stdout: "ignored"}, {Stdout: "second"}}}

	if err := f.RunInteractive("tmux", "attach-session", "-t", "$3"); err != nil {
		t.Fatalf("RunInteractive returned error: %v", err)
	}
	stdout, _, err := f.Run("tmux", "list-sessions")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if stdout != "second" {
		t.Errorf("stdout = %q, want %q — both methods must share one response queue", stdout, "second")
	}

	wantCalls := []Call{
		{Name: "tmux", Args: []string{"attach-session", "-t", "$3"}, Interactive: true},
		{Name: "tmux", Args: []string{"list-sessions"}},
	}
	if !reflect.DeepEqual(f.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", f.Calls, wantCalls)
	}
}
