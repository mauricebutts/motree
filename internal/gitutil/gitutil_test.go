package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mauricebutts/motree/internal/executil"
)

// runGit runs git in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// setupRepo creates a bare "origin" repo whose default branch is
// defaultBranch, seeded with one commit, plus a real local clone ("work")
// of it. It returns the clone's path.
func setupRepo(t *testing.T, defaultBranch string) (workDir string) {
	t.Helper()
	tmp := t.TempDir()
	originDir := filepath.Join(tmp, "origin.git")
	seedDir := filepath.Join(tmp, "seed")
	workDir = filepath.Join(tmp, "work")

	runGit(t, tmp, "init", "--bare", "-q", originDir)
	runGit(t, tmp, "clone", "-q", originDir, seedDir)
	runGit(t, seedDir, "checkout", "-b", defaultBranch, "-q")
	if err := os.WriteFile(filepath.Join(seedDir, "f.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGit(t, seedDir, "add", "f.txt")
	runGit(t, seedDir, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-q", "-m", "init")
	runGit(t, seedDir, "push", "-q", "origin", defaultBranch)
	// Point the bare origin's HEAD at the default branch so clones pick it
	// up as refs/remotes/origin/HEAD.
	runGit(t, originDir, "symbolic-ref", "HEAD", "refs/heads/"+defaultBranch)
	runGit(t, tmp, "clone", "-q", originDir, workDir)

	return workDir
}

func TestRepoRoot_InsideRepo_ReturnsToplevel(t *testing.T) {
	workDir := setupRepo(t, "main")
	// Resolve symlinks (e.g. macOS /private/tmp vs /tmp) the same way git does.
	wantRoot, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	g := Git{Runner: executil.RealRunner{}}
	got, err := g.RepoRoot(workDir)
	if err != nil {
		t.Fatalf("RepoRoot returned error: %v", err)
	}
	if got != wantRoot {
		t.Errorf("RepoRoot(%q) = %q, want %q", workDir, got, wantRoot)
	}
}

func TestRepoRoot_OutsideRepo_ReturnsEmpty(t *testing.T) {
	outside := t.TempDir()

	g := Git{Runner: executil.RealRunner{}}
	got, err := g.RepoRoot(outside)
	if err != nil {
		t.Fatalf("RepoRoot returned error: %v", err)
	}
	if got != "" {
		t.Errorf("RepoRoot(%q) = %q, want empty string", outside, got)
	}
}

func TestDefaultBranch_UsesOriginHEAD_FallsBackToMain(t *testing.T) {
	// origin/HEAD present, pointing at a non-"main" branch: must reflect
	// the actual branch name, not a hardcoded fallback.
	workDir := setupRepo(t, "trunk")
	g := Git{Runner: executil.RealRunner{}}

	got := g.DefaultBranch(workDir)
	if got != "trunk" {
		t.Errorf("DefaultBranch() = %q, want %q", got, "trunk")
	}

	// Remove origin/HEAD: must fall back to "main".
	runGit(t, workDir, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
	got = g.DefaultBranch(workDir)
	if got != "main" {
		t.Errorf("DefaultBranch() after removing origin/HEAD = %q, want %q", got, "main")
	}
}

func TestWorktreeAddNewBranch_CreatesBranchOffBaseAndUnsetsUpstream(t *testing.T) {
	workDir := setupRepo(t, "trunk")
	g := Git{Runner: executil.RealRunner{}}
	worktreePath := filepath.Join(filepath.Dir(workDir), "wt-newbranch")

	err := g.WorktreeAddNewBranch(workDir, worktreePath, "feature-x", "origin/trunk")
	if err != nil {
		t.Fatalf("WorktreeAddNewBranch: %v", err)
	}

	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("worktree dir missing: %v", err)
	}
	if !g.BranchExists(workDir, "feature-x") {
		t.Errorf("expected branch feature-x to exist")
	}

	out := runGit(t, worktreePath, "branch", "-vv")
	var featureLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "feature-x") {
			featureLine = line
			break
		}
	}
	if featureLine == "" {
		t.Fatalf("branch -vv output missing feature-x line: %s", out)
	}
	if strings.Contains(featureLine, "[origin/trunk]") {
		t.Errorf("expected upstream to be unset, feature-x branch -vv line: %s", featureLine)
	}
}

func TestWorktreeAdd_ExistingBranch_Reused(t *testing.T) {
	workDir := setupRepo(t, "trunk")
	g := Git{Runner: executil.RealRunner{}}

	runGit(t, workDir, "branch", "existing-branch")
	worktreePath := filepath.Join(filepath.Dir(workDir), "wt-existing")

	err := g.WorktreeAdd(workDir, worktreePath, "existing-branch")
	if err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	out := strings.TrimSpace(runGit(t, worktreePath, "branch", "--show-current"))
	if out != "existing-branch" {
		t.Errorf("worktree checked out branch = %q, want %q", out, "existing-branch")
	}
}

func TestWorktreeAddDetached_CreatesDetachedWorktree(t *testing.T) {
	workDir := setupRepo(t, "trunk")
	g := Git{Runner: executil.RealRunner{}}
	worktreePath := filepath.Join(filepath.Dir(workDir), "wt-detached")

	err := g.WorktreeAddDetached(workDir, worktreePath)
	if err != nil {
		t.Fatalf("WorktreeAddDetached: %v", err)
	}

	out := strings.TrimSpace(runGit(t, worktreePath, "branch", "--show-current"))
	if out != "" {
		t.Errorf("expected detached HEAD (no current branch), got %q", out)
	}
}

func TestFetchOrigin_FetchesBranch(t *testing.T) {
	workDir := setupRepo(t, "trunk")
	g := Git{Runner: executil.RealRunner{}}

	if err := g.FetchOrigin(workDir, "trunk"); err != nil {
		t.Fatalf("FetchOrigin: %v", err)
	}
}

func TestWorktreePrune_RemovesStaleAdminData(t *testing.T) {
	workDir := setupRepo(t, "trunk")
	g := Git{Runner: executil.RealRunner{}}
	worktreePath := filepath.Join(filepath.Dir(workDir), "wt-stale")

	if err := g.WorktreeAddDetached(workDir, worktreePath); err != nil {
		t.Fatalf("setup WorktreeAddDetached: %v", err)
	}
	// Remove the worktree dir out from under git, leaving stale admin data.
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	if err := g.WorktreePrune(workDir); err != nil {
		t.Fatalf("WorktreePrune: %v", err)
	}

	out := runGit(t, workDir, "worktree", "list")
	if strings.Contains(out, worktreePath) {
		t.Errorf("expected stale worktree to be pruned, worktree list: %s", out)
	}
}

func TestBranchDelete_RemovesLocalBranch(t *testing.T) {
	workDir := setupRepo(t, "trunk")
	g := Git{Runner: executil.RealRunner{}}

	runGit(t, workDir, "branch", "to-delete")
	if err := g.BranchDelete(workDir, "to-delete"); err != nil {
		t.Fatalf("BranchDelete: %v", err)
	}
	if g.BranchExists(workDir, "to-delete") {
		t.Errorf("expected branch to-delete to be gone")
	}
}

func TestWorktreeRemove_Failure_ReturnsErrorWithGitStderr(t *testing.T) {
	workDir := setupRepo(t, "trunk")
	g := Git{Runner: executil.RealRunner{}}
	worktreePath := filepath.Join(filepath.Dir(workDir), "wt-dirty")

	if err := g.WorktreeAddDetached(workDir, worktreePath); err != nil {
		t.Fatalf("setup WorktreeAddDetached: %v", err)
	}
	// Make removal fail: an untracked file inside the worktree.
	if err := os.WriteFile(filepath.Join(worktreePath, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	err := g.WorktreeRemove(workDir, worktreePath)
	if err == nil {
		t.Fatalf("WorktreeRemove: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "contains modified or untracked files") {
		t.Errorf("WorktreeRemove error = %q, want it to contain git's actual stderr text", err.Error())
	}
}
