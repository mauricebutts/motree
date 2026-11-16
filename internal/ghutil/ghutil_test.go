package ghutil

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mauricebutts/motree/internal/executil"
)

func TestPRHeadRef_ParsesJQOutput(t *testing.T) {
	runner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: "feature-branch\n"},
		},
	}
	g := GH{Runner: runner}

	ref, err := g.PRHeadRef("/repo/root", "42")
	if err != nil {
		t.Fatalf("PRHeadRef returned error: %v, want nil", err)
	}
	if ref != "feature-branch" {
		t.Errorf("PRHeadRef = %q, want %q", ref, "feature-branch")
	}

	wantCalls := []executil.Call{
		{Name: "sh", Args: []string{"-c", "cd '/repo/root' && 'gh' 'pr' 'view' '42' '--json' 'headRefName' '--jq' '.headRefName'"}},
	}
	if !reflect.DeepEqual(runner.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", runner.Calls, wantCalls)
	}
}

func TestPRHeadRef_EmptyOnFailure(t *testing.T) {
	runner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: errors.New("gh: not found")},
		},
	}
	g := GH{Runner: runner}

	ref, err := g.PRHeadRef("/repo/root", "999")
	if err != nil {
		t.Fatalf("PRHeadRef returned error: %v, want nil (failures surface as empty string, not an error)", err)
	}
	if ref != "" {
		t.Errorf("PRHeadRef = %q, want empty string", ref)
	}
}

func TestPRHeadRef_EmptyOnEmptyOutput(t *testing.T) {
	runner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Stdout: ""},
		},
	}
	g := GH{Runner: runner}

	ref, err := g.PRHeadRef("/repo/root", "7")
	if err != nil {
		t.Fatalf("PRHeadRef returned error: %v, want nil", err)
	}
	if ref != "" {
		t.Errorf("PRHeadRef = %q, want empty string", ref)
	}
}

func TestCheckout_RunsGhPrCheckoutInWorktreeDir(t *testing.T) {
	runner := &executil.FakeRunner{
		Queue: []executil.Response{
			{},
		},
	}
	g := GH{Runner: runner}

	if err := g.Checkout("/repo/.claude/worktrees/pr-42", "42"); err != nil {
		t.Fatalf("Checkout returned error: %v, want nil", err)
	}

	wantCalls := []executil.Call{
		{Name: "sh", Args: []string{"-c", "cd '/repo/.claude/worktrees/pr-42' && 'gh' 'pr' 'checkout' '42'"}},
	}
	if !reflect.DeepEqual(runner.Calls, wantCalls) {
		t.Errorf("Calls = %+v, want %+v", runner.Calls, wantCalls)
	}
}

func TestCheckout_PropagatesError(t *testing.T) {
	wantErr := errors.New("gh: checkout failed")
	runner := &executil.FakeRunner{
		Queue: []executil.Response{
			{Err: wantErr},
		},
	}
	g := GH{Runner: runner}

	err := g.Checkout("/repo/.claude/worktrees/pr-42", "42")
	if err == nil {
		t.Fatalf("Checkout returned nil error, want the underlying gh error")
	}
}

// withLookPath overrides the package-level LookPath var for the duration
// of the test, restoring it afterward, so Available()'s result doesn't
// depend on whether gh actually happens to be installed on the machine
// running `go test`.
func withLookPath(t *testing.T, fn func(file string) (string, error)) {
	t.Helper()
	orig := LookPath
	LookPath = fn
	t.Cleanup(func() { LookPath = orig })
}

func TestAvailable_TrueWhenLookPathSucceeds(t *testing.T) {
	withLookPath(t, func(file string) (string, error) { return "/usr/bin/" + file, nil })

	if !Available() {
		t.Errorf("Available() = false, want true")
	}
}

func TestAvailable_FalseWhenLookPathFails(t *testing.T) {
	withLookPath(t, func(file string) (string, error) { return "", errors.New("not found") })

	if Available() {
		t.Errorf("Available() = true, want false")
	}
}
