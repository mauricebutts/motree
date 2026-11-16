package executil

import (
	"bytes"
	"fmt"
	"os/exec"
)

// Runner is the interface for executing commands.
type Runner interface {
	Run(name string, args ...string) (stdout, stderr string, err error)
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
