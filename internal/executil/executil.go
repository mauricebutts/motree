package executil

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

// Runner is the interface for executing commands.
//
// Run is the workhorse: it captures stdout/stderr and gives the child no
// stdin at all, which is what every git/gh/tmux/fzf call in this tool
// wants. RunInteractive is the deliberate narrow escape hatch for the one
// command that cannot work that way — "tmux attach-session", which needs
// its stdin to be a real terminal or it dies with "open terminal failed:
// not a terminal". Nothing else should use it; see RealRunner.RunInteractive.
type Runner interface {
	Run(name string, args ...string) (stdout, stderr string, err error)
	RunInteractive(name string, args ...string) error
}

// RealRunner executes commands by shelling out via os/exec.
type RealRunner struct{}

// Run executes a command and returns stdout, stderr, and error.
// On non-zero exit, the error message includes the captured stderr.
func (r RealRunner) Run(name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command(name, args...)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

	// If there's an exit error, wrap it with stderr context
	if err != nil {
		err = fmt.Errorf("%w\nstderr: %s", err, stderr)
	}

	return stdout, stderr, err
}

// RunInteractive executes a command with this process's real stdin, stdout,
// and stderr inherited directly, so the child shares our controlling
// terminal. It captures nothing and returns only the command's error.
//
// This exists solely for "tmux attach-session", which takes the terminal
// over for the lifetime of the attached session: run through Run instead,
// its stdin would be nil and its stdout a bytes.Buffer, and tmux would
// immediately fail with "open terminal failed: not a terminal" — so
// `mt <name>`, `mt pr` and `mt ls` could never attach from outside tmux.
// (The "switch-client" branch is unaffected: it just messages an
// already-running tmux server and needs no tty of its own.)
//
// Because it hands our terminal over, callers must have already persisted
// any state they care about before calling it — see mtcmd.saveAndAttach.
func (r RealRunner) RunInteractive(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
