package executil

import (
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
