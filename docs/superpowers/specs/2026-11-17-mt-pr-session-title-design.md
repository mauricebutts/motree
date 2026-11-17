# `mt pr` optional session title — design

## Intent

`mt pr <pr_number>` currently hard-codes the session name to
`pr-<pr_number>`. That name is the only handle the session has in `mt ls`,
`mt rm` and tmux, and a bare PR number says nothing about what the review
is *for*. This adds an optional third argument that names the session
whatever the user wants:

```
mt pr 420 review-care-spike
```

Everything else about `mt pr` is unchanged.

## Goals

- `mt pr <pr_number> <title>` creates the session, worktree and tmux
  session under `<title>` instead of `pr-<pr_number>`.
- A session records which PR (in which repo) it is tied to, and any later
  attempt to create a *second* session for that same PR fails with an
  error naming the session that already holds it.
- The PR number stays visible in `mt ls` — the title replaces the name,
  not the `PR #420` description.
- Existing `~/.motree` files keep working untouched, including sessions
  created by today's `mt pr`.

## Non-goals

- Renaming a session after the fact. Pick the title at creation; otherwise
  use `mt rm` and re-create.
- Titles for `mt <name>` / `mt spawn` — those already take the name
  directly.
- Looking a PR up across repos. `~/.motree` is global, so the tie is
  always keyed on (repo root, PR number).
- Re-attaching by PR number. A bare `mt pr 420` after the session has been
  titled does **not** find and attach `review-care-spike` — it errors and
  names it, and the user attaches with `mt review-care-spike` or `mt ls`.
  Deliberate: it keeps the PR tie a one-line guard instead of a second way
  to resolve a session name.
- Fixing `mt rm`'s branch deletion for PR sessions. It calls
  `BranchDelete(repoRoot, name)`, which for a PR session has never matched
  the real head branch (`gh pr checkout` names it after the PR's branch,
  e.g. `feature/ASF-815-…`). That delete is best-effort and already
  silently no-ops today; a custom title changes nothing about it.

## Anti-goals

What would make this a failure even if it technically works:

- Two worktrees for the same PR in one repo. The second one's
  `gh pr checkout` fails with git's "already checked out at …", which is
  exactly the confusing failure this feature must not introduce. The guard
  replaces that failure with a message naming the session that holds it.
- A display string becoming load-bearing. `Description` is shown to the
  user and overwritable; it must not be parsed back as a key.

## Constraints

- `~/.motree` is a JSON file shared with sessions created by older builds.
  Any new field is additive and `omitempty`.
- `name` in `mtcmd.PR` drives four things at once — tmux session name,
  worktree directory, state key, and the `ensureSessionWindow` target. The
  title replaces all four together; they do not get to disagree.
- A session's `Repos` is a slice: one session can span several repos. The
  PR number can't be a single per-session value.

## Approaches

Considered, with the lookup key for a bare `mt pr 420` in mind:

| | Approach | Trade-off |
|---|---|---|
| A | `PRNumber` on `state.Session` | Smallest change, but wrong shape — one session can cover several repos, so a single per-session PR number breaks as soon as it does. |
| **B** | **`PRNumber` on `state.Repo`** | **Chosen.** The PR is a per-repo fact, and (repo path, PR number) is exactly the unique key the lookup needs. Costs one `EnsureRepoPR` next to `EnsureRepo`. |
| C | Parse `PR #420` back out of `Description` | No schema change, but makes display text load-bearing — `mt ls` shows it and `-d` can overwrite it. Rejected. |

Chosen: **B**, agreed in conversation.

## Design

**State** (`internal/state`)

```go
type Repo struct {
    RepoPath     string `json:"repo_path"`
    WorktreePath string `json:"worktree_path"`
    PRNumber     string `json:"pr_number,omitempty"`
}
```

- `EnsureRepoPR(sessionName, repoPath, worktreePath, prNumber)` — as
  `EnsureRepo`, but also records `PRNumber`. `EnsureRepo` keeps its current
  signature and leaves `PRNumber` empty; every non-PR caller is unaffected.
- `SessionForPR(repoPath, prNumber) string` — returns the name of the
  session holding a repo matching both, or `""`. Empty `prNumber` never
  matches, so pre-existing rows (no `pr_number`) can't be hit by accident.

**`mtcmd.PR`** gains a `title` parameter. The name is simply
`title` when given, else `"pr-" + prNumber` — the PR tie never resolves a
name, it only guards.

The guard goes inside the existing "worktree doesn't exist yet" branch,
before the `gh` lookup:

```go
if existing := d.State.SessionForPR(repoRoot, prNumber); existing != "" && existing != name {
    return fmt.Errorf("PR %s is already checked out as session '%s'", prNumber, existing)
}
```

It returns before any worktree, tmux session, state write or `gh` call.

Idempotent re-runs are unaffected, because the guard sits behind the
`os.Stat(worktreePath)` check that is already there: re-running
`mt pr 420 review-care-spike` finds that worktree and re-attaches without
ever reaching the guard. The `existing != name` clause covers only the
case where state still holds the tie but the worktree was removed by hand.

The description stays `"PR #" + prNumber` regardless of the title, so
`mt ls` still shows which PR a session is for. `UpsertSession` is then
called with the resolved `name`, and `EnsureRepoPR` replaces today's
`EnsureRepo` call.

**CLI** (`cmd/mt/main.go`)

- `runPR` takes an optional second positional: `mt pr <pr_number> [<title>]`.
  Zero args is still the usage error; three or more is a usage error too.
- `usage()` and `printHelp()` updated to show the optional title.

## What's left to the builder

- Whether `SessionForPR` lives as a method on `*State` next to `IsTracked`
  or as a free function — house style decides.
- Exact wording of the usage/help lines, following the existing text.
- Whether to validate the title (reject digits-only, so `mt pr 420 421`
  can't silently mean a title of `421`). Recommended, as a usage error;
  the builder may skip it if it complicates `runPR` disproportionately.
