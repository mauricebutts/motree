package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mauricebutts/motree/internal/state"
)

// mtBinaryPath is the path to the mt binary built once by TestMain, shared
// by every test in this file via os/exec — these are smoke/integration
// tests of the real compiled CLI, not of mtcmd's functions directly (those
// have their own unit tests, with fake git/tmux runners, in
// internal/mtcmd).
var mtBinaryPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mt-build-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	name := "mt"
	if runtime.GOOS == "windows" {
		name = "mt.exe"
	}
	mtBinaryPath = filepath.Join(dir, name)

	cmd := exec.Command("go", "build", "-o", mtBinaryPath, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("go build ./cmd/mt failed: " + err.Error() + "\n" + string(out))
	}

	os.Exit(m.Run())
}

// runResult is the outcome of one built-binary invocation.
type runResult struct {
	stdout, stderr string
	exitCode       int
}

// runMt runs the built mt binary with args, in dir, with env as its
// complete environment (callers build it explicitly — no ambient
// inheritance — so each test's isolation, e.g. which PATH entries and
// which MOTREE_STATE_FILE are visible, is exact and not accidental).
func runMt(t *testing.T, dir string, env []string, args ...string) runResult {
	t.Helper()

	cmd := exec.Command(mtBinaryPath, args...)
	cmd.Dir = dir
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("failed to run mt binary: %v", err)
		}
		exitCode = exitErr.ExitCode()
	}

	return runResult{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitCode}
}

// baseEnv returns a minimal, explicit environment for a built-binary
// invocation that doesn't need git/tmux/fzf (help/usage text only):
// just PATH (to find any subprocess at all) and a MOTREE_STATE_FILE
// pointed at a fresh, never-touched path, so a stray newDeps() call can
// never read or write the real ~/.motree.
func baseEnv(t *testing.T) []string {
	t.Helper()
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"MOTREE_STATE_FILE=" + filepath.Join(t.TempDir(), "state.json"),
	}
}

func TestMain_HelpTextListsAllSubcommands(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			res := runMt(t, t.TempDir(), baseEnv(t), arg)
			if res.exitCode != 0 {
				t.Fatalf("mt %s: exit code = %d, want 0; stdout=%q stderr=%q", arg, res.exitCode, res.stdout, res.stderr)
			}

			combined := res.stdout + res.stderr
			for _, want := range []string{"mt", "spawn", "pr", "ls", "rm"} {
				if !strings.Contains(combined, want) {
					t.Errorf("mt %s: output missing %q; got:\n%s", arg, want, combined)
				}
			}
		})
	}
}

func TestMain_NoArgs_PrintsUsageAndExitsNonZero(t *testing.T) {
	res := runMt(t, t.TempDir(), baseEnv(t))

	if res.exitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero")
	}

	combined := strings.ToLower(res.stdout + res.stderr)
	if !strings.Contains(combined, "usage") {
		t.Errorf("expected usage text on a no-args invocation; got stdout=%q stderr=%q", res.stdout, res.stderr)
	}
}

// writeStub writes an executable shell script named name into dir,
// containing body (its argv is available as $1, $2, ... as usual).
func writeStub(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("writing stub %s: %v", name, err)
	}
}

// runGit runs a real git command with dir as its working directory, for
// test setup (building the throwaway repo create/rm operate against).
// This is deliberately real git, not a stub — only tmux and fzf need
// stubbing below, since they're the two tools that would otherwise need an
// interactive terminal (a real attach/switch-client, a real fzf picker)
// that an automated test can't drive.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v (in %s) failed: %v\n%s", args, dir, err, out)
	}
}

// tmuxStub fakes just enough of tmux's CLI surface for Create's and
// RemoveSession's tmux calls to all succeed without ever needing a real
// terminal: list-sessions always reports none (so FindSessionID always
// returns "", which both Create's ensureSessionWindow and RemoveSession's
// best-effort kill-session handle as "no session" rather than an error),
// new-session prints a fixed fake session id (its "-P -F #{session_id}"
// output), and every other subcommand (new-window, split-window,
// send-keys, attach-session, switch-client, kill-session, ...) is a no-op
// success — standing in for the real tmux session/pane/attach machinery
// mtcmd's own fake-Runner unit tests already cover in detail.
const tmuxStub = `case "$1" in
  list-sessions) exit 1 ;;
  new-session) echo '$fake-session' ;;
  *) exit 0 ;;
esac
`

// fzfStub fakes fzf for Rm's picker: fzfutil.Run feeds it the candidate
// rows on stdin and expects the chosen row on stdout, so this stub just
// always "picks" the first row — standing in for a real interactive
// fzf session, which (like a real tmux attach) an automated test can't
// drive without a terminal.
const fzfStub = `head -n 1
`

func TestMain_CreateAndRm_PersistStateToDiskAcrossInvocations(t *testing.T) {
	stubDir := t.TempDir()
	writeStub(t, stubDir, "tmux", tmuxStub)
	writeStub(t, stubDir, "fzf", fzfStub)

	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-q")
	runGit(t, repoDir, "config", "user.email", "mt-test@example.com")
	runGit(t, repoDir, "config", "user.name", "mt test")
	runGit(t, repoDir, "commit", "--allow-empty", "-q", "-m", "init")
	// Pre-create the session's branch so Create takes the "existing branch"
	// worktree-add path, which needs no "origin" remote at all (the
	// alternative path would try to branch off "origin/<base>", which
	// doesn't exist in this bare throwaway repo).
	runGit(t, repoDir, "branch", "demo-session")

	home := t.TempDir()
	stateFile := filepath.Join(t.TempDir(), "state.json")
	env := []string{
		"PATH=" + stubDir + ":" + os.Getenv("PATH"),
		"HOME=" + home,
		"MOTREE_STATE_FILE=" + stateFile,
	}

	if _, err := os.Stat(stateFile); err == nil {
		t.Fatalf("state file %s already exists before the test even starts", stateFile)
	}

	createRes := runMt(t, repoDir, env, "demo-session")
	if createRes.exitCode != 0 {
		t.Fatalf("mt demo-session failed: exit=%d stdout=%q stderr=%q", createRes.exitCode, createRes.stdout, createRes.stderr)
	}

	afterCreate, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("state file wasn't created by `mt demo-session`: %v", err)
	}

	var stAfterCreate state.State
	if err := json.Unmarshal(afterCreate, &stAfterCreate); err != nil {
		t.Fatalf("state file after create isn't valid JSON: %v\ncontent: %s", err, afterCreate)
	}
	if len(stAfterCreate.Sessions) != 1 || stAfterCreate.Sessions[0].Name != "demo-session" {
		t.Fatalf("state.Sessions after create = %+v, want exactly one session named demo-session", stAfterCreate.Sessions)
	}

	rmRes := runMt(t, repoDir, env, "rm")
	if rmRes.exitCode != 0 {
		t.Fatalf("mt rm failed: exit=%d stdout=%q stderr=%q", rmRes.exitCode, rmRes.stdout, rmRes.stderr)
	}

	afterRm, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("state file missing after `mt rm`: %v", err)
	}

	if bytes.Equal(afterCreate, afterRm) {
		t.Fatalf("state file content is unchanged after `mt rm` — Save was not called on rm's success path")
	}

	var stAfterRm state.State
	if err := json.Unmarshal(afterRm, &stAfterRm); err != nil {
		t.Fatalf("state file after rm isn't valid JSON: %v\ncontent: %s", err, afterRm)
	}
	if len(stAfterRm.Sessions) != 0 {
		t.Fatalf("state.Sessions after rm = %+v, want none — rm should have removed 'demo-session'", stAfterRm.Sessions)
	}

	// The worktree Create actually made on disk should be gone too —
	// confirms RemoveSession's real `git worktree remove` ran, not just
	// the state bookkeeping.
	worktreePath := filepath.Join(repoDir, ".claude", "worktrees", "demo-session")
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Errorf("worktree %s still exists after `mt rm`, want it removed", worktreePath)
	}
}
