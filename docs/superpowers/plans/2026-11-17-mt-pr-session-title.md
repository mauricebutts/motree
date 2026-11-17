# `mt pr` optional session title Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `mt pr <pr_number> [<title>]` name the session, and refuse to create a second session for a PR that one already holds.

**Architecture:** `state.Repo` gains a `PRNumber` field, so a session's tie to a PR is a per-repo fact keyed on (repo path, PR number). `mtcmd.PR` takes a `title` and uses it as the session name when non-empty; a one-line guard inside the existing "worktree doesn't exist yet" branch returns an error naming the session that already holds the PR. The guard never resolves a name — it only refuses.

**Tech Stack:** Go 1.x, stdlib only. `go test ./...` for everything.

**Spec:** `docs/superpowers/specs/2026-11-17-mt-pr-session-title-design.md`

## Global Constraints

- New state fields are additive and `omitempty`; an existing `~/.motree` written by today's build must load and behave unchanged.
- Error text is exactly: `PR 420 is already checked out as session 'review-care-spike'` — single quotes around the session name, no `mt:` prefix (`cmd/mt/main.go`'s `reportAndExit` adds it).
- The session description stays `"PR #" + prNumber` regardless of title.
- `EnsureRepo` keeps its current three-argument signature; `create.go` and `spawn.go` are not touched.
- Conventional commit prefixes, matching the repo's history (`feat:`, `fix:`).

## Review Focus

Input classes the spec implies but that no task's happy path exercises. Each has a test pinned to the task that owns the code.

- A digits-only title (`mt pr 420 421`) would silently name a session `421` — Task 3 rejects it as a usage error.
- An old `~/.motree` row has no `pr_number`; `SessionForPR` must never match it — Task 1.
- `SessionForPR(repo, "")` must never match, or every PR-less repo row becomes a false positive — Task 1.
- The same PR number in two different repos must not collide — Task 1.
- State holds the tie but the worktree was deleted by hand: re-running the *same* invocation must rebuild, not error — Task 2.

---

### Task 1: Per-repo PR number in state

**Files:**
- Modify: `internal/state/state.go` (the `Repo` struct at :13-16, and new funcs beside `EnsureRepo` at :99)
- Test: `internal/state/state_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Repo struct { RepoPath, WorktreePath string; PRNumber string }` with tags `json:"repo_path"`, `json:"worktree_path"`, `json:"pr_number,omitempty"`
  - `func (s *State) EnsureRepoPR(sessionName, repoPath, worktreePath, prNumber string)`
  - `func (s *State) SessionForPR(repoPath, prNumber string) string`

- [ ] **Step 1: Write the failing tests**

```go
func TestEnsureRepoPR_RecordsPRNumberOnTheRepoRow(t *testing.T)
// UpsertSession("review-care-spike", "PR #420", 1); EnsureRepoPR(same, "/repo1", "/wt1", "420")
// want ReposFor("review-care-spike") == []Repo{{RepoPath: "/repo1", WorktreePath: "/wt1", PRNumber: "420"}}

func TestSessionForPR_FindsSessionHoldingThatRepoAndPR(t *testing.T)
// two sessions; want SessionForPR("/repo1", "420") == "review-care-spike"

func TestSessionForPR_SamePRNumberInAnotherRepoDoesNotMatch(t *testing.T)
// EnsureRepoPR("a", "/repo1", "/wt1", "420"); want SessionForPR("/repo2", "420") == ""

func TestSessionForPR_IgnoresRowsWithNoPRNumber(t *testing.T)
// EnsureRepo("legacy", "/repo1", "/wt1")  // no PR number, as older builds wrote
// want SessionForPR("/repo1", "420") == ""

func TestSessionForPR_EmptyPRNumberNeverMatches(t *testing.T)
// EnsureRepo("legacy", "/repo1", "/wt1"); want SessionForPR("/repo1", "") == ""

func TestSaveLoad_RoundTripPreservesPRNumber(t *testing.T)
// Save then Load a state carrying PRNumber: "420"; want it back
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/state/ -run 'TestEnsureRepoPR|TestSessionForPR|TestSaveLoad_RoundTripPreservesPRNumber' -v`
Expected: FAIL — `EnsureRepoPR`/`SessionForPR` undefined, `PRNumber` unknown field.

- [ ] **Step 3: Add the `PRNumber` field and implement both functions in `internal/state/state.go`**

`EnsureRepoPR` mirrors `EnsureRepo`'s existing shape (find session by name, no-op if a row for `repoPath` already exists, else append) with `PRNumber` set on the appended row. `SessionForPR` returns `""` immediately when `prNumber == ""`, then scans sessions and their repos for a row matching both `RepoPath` and `PRNumber`.

- [ ] **Step 4: Run the package suite**

Run: `go test ./internal/state/`
Expected: PASS, including the pre-existing `EnsureRepo`/`ReposFor`/round-trip tests.

- [ ] **Step 5: Commit**

```bash
git add internal/state/state.go internal/state/state_test.go
git commit -m "feat: track a session's PR number per repo in state"
```

---

### Task 2: `mtcmd.PR` takes a title and guards the PR tie

**Files:**
- Modify: `internal/mtcmd/pr.go`
- Test: `internal/mtcmd/pr_test.go`

**Interfaces:**
- Consumes: `(*state.State).SessionForPR`, `(*state.State).EnsureRepoPR` from Task 1.
- Produces: `func PR(d *Deps, gh ghutil.GH, prNumber, title, cwd string, insideTmux bool, now int64) error` — `title` is the new parameter, inserted after `prNumber`.

- [ ] **Step 1: Update the six existing `PR(...)` call sites in `internal/mtcmd/pr_test.go` to pass `""` for `title`**

Mechanical: every current call becomes `PR(d, gh, prNumber, "", repoRoot, false, 1)`. Behavior of those tests is unchanged — an empty title must keep today's `pr-<n>` naming.

- [ ] **Step 2: Write the failing tests**

Model the two creating tests on `TestPR_HappyPath_DetachedWorktreeThenGhCheckout`, which supplies the full git/tmux/gh `FakeRunner` queues and calls `setClaudeHome(t)`. Nothing is written to disk in these tests — the worktree is asserted through the recorded `git worktree add --detach <path>` argument, not `os.Stat`.

```go
func TestPR_TitleNamesSessionWorktreeAndState(t *testing.T)
// PR(d, gh, "420", "review-care-spike", repoRoot, false, 1)
// want: git call "worktree add --detach <repoRoot>/.claude/worktrees/review-care-spike"
//       tmux new-session called with session name "review-care-spike"
//       d.State.Sessions[0].Name == "review-care-spike"
//       d.State.Sessions[0].Description == "PR #420"   // title replaces the name, not the description

func TestPR_RecordsPRNumberAgainstTheRepo(t *testing.T)
// same setup; after the call, want d.State.SessionForPR(repoRoot, "420") == "review-care-spike"

func TestPR_BarePRNumberWhenAlreadyTitled_ReturnsErrorAndCreatesNothing(t *testing.T)
// seed: UpsertSession("review-care-spike", "PR #420", 1) + EnsureRepoPR(same, repoRoot, wt, "420")
// PR(d, gh, "420", "", repoRoot, false, 1)
// want err.Error() == `PR 420 is already checked out as session 'review-care-spike'`
// and: zero gh calls, git calls == rev-parse only, zero tmux calls, Sessions still len 1

func TestPR_DifferentTitleForSamePR_ReturnsErrorAndCreatesNothing(t *testing.T)
// same seed; PR(d, gh, "420", "other-name", repoRoot, false, 1) -> same error, nothing created

func TestPR_SameTitleWithMissingWorktree_RebuildsRatherThanErroring(t *testing.T)
// seed the tie for "review-care-spike", with full happy-path runner queues and no worktree on disk
// PR(d, gh, "420", "review-care-spike", repoRoot, false, 1)
// want nil error, and the gh lookup + "worktree add --detach" to have run
```

`forceGhAvailable` and `setClaudeHome` already exist in the file. `assertPRLookupOnly` expects exactly one gh call, so the two "creates nothing" tests — which expect zero — assert inline instead of using it.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/mtcmd/ -run TestPR_ -v`
Expected: FAIL — `PR` takes 6 arguments, not 7.

- [ ] **Step 4: Implement the title and the guard in `internal/mtcmd/pr.go`**

Signature gains `title`. `name` becomes `title` when non-empty, else `"pr-" + prNumber`; `worktreePath` continues to derive from `name`. Inside the existing `os.Stat(worktreePath)` is-not-exist branch, before the `gh.PRHeadRef` call, return `fmt.Errorf("PR %s is already checked out as session '%s'", prNumber, existing)` when `d.State.SessionForPR(repoRoot, prNumber)` is non-empty and differs from `name`. Swap the `EnsureRepo` call for `EnsureRepoPR` with `prNumber`. Update the doc comment to describe the title and the guard.

- [ ] **Step 5: Run the package suite**

Run: `go test ./internal/mtcmd/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mtcmd/pr.go internal/mtcmd/pr_test.go
git commit -m "feat: let mt pr name its session, refuse a duplicate PR session"
```

---

### Task 3: CLI argument, usage and help

**Files:**
- Modify: `cmd/mt/main.go` (`runPR` at :431, `usage()` at :129, `printHelp()` at :134)
- Test: `cmd/mt/main_test.go`

**Interfaces:**
- Consumes: `mtcmd.PR(d, gh, prNumber, title, cwd, insideTmux, now)` from Task 2.
- Produces: nothing for later tasks.

- [ ] **Step 1: Write the failing tests**

```go
func TestMain_PR_TitleArgNamesTheSession(t *testing.T)
// gh + tmux stubs as in the existing PR tests; runMt(..., "pr", "420", "review-care-spike")
// want the state file to hold a session named "review-care-spike" with description "PR #420"

func TestMain_PR_DigitsOnlyTitle_IsUsageError(t *testing.T)
// runMt(..., "pr", "420", "421") -> exit 1, usage line on stderr, no session created

func TestMain_PR_TooManyArgs_IsUsageError(t *testing.T)
// runMt(..., "pr", "420", "a", "b") -> exit 1, usage line on stderr
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/mt/ -run TestMain_PR_ -v`
Expected: FAIL — the title argument is ignored, so no `review-care-spike` session and no usage error.

- [ ] **Step 3: Implement argument handling in `runPR`**

Accept one or two positionals. Zero, three or more → `usage()` and `errUsageShown`, as today. A second positional matching `prNumberPattern`'s shape (digits only) → `usage()` and `errUsageShown`, so `mt pr 420 421` can't mean a title of `421`; `mtcmd` already exports nothing for this, so test the digits locally in `main.go`. Pass the title through to `mtcmd.PR`.

- [ ] **Step 4: Update `usage()` and `printHelp()`**

`usage()`: `mt pr <pr_number> [<title>]`. `printHelp()`: add a line to the existing `mt pr` paragraph saying the optional title names the session instead of `pr-<pr_number>`, and that a PR already checked out under another session is refused by name.

- [ ] **Step 5: Run the full suite and vet**

Run: `go vet ./... && go test ./...`
Expected: PASS, all packages.

- [ ] **Step 6: Commit**

```bash
git add cmd/mt/main.go cmd/mt/main_test.go
git commit -m "feat: accept an optional session title on mt pr"
```

---

### Task 4: Manual verification against a real repo

**Files:** none — verification only.

- [ ] **Step 1: Build to a scratch path**

Run: `go build -o /tmp/mt-title ./cmd/mt`

- [ ] **Step 2: Exercise the three paths in a repo with a reachable PR**

With `TMUX` unset so no client is switched:
1. `mt pr <n> <title>` — creates `.claude/worktrees/<title>`, session named `<title>`, `mt ls` shows `PR #<n>`.
2. `mt pr <n>` — errors `PR <n> is already checked out as session '<title>'`, creates nothing.
3. `mt rm` the session, confirm the state row and worktree are gone.

- [ ] **Step 3: Confirm the pre-existing `~/.motree` still loads**

Run `mt ls` and check the sessions written by older builds are all still listed.
