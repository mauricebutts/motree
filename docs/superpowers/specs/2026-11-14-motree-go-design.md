# motree Go rewrite — design

## Intent

`mt` (motree) is a personal CLI that orchestrates git worktrees and tmux
sessions for doing parallel coding-agent work across one or more repos. It
currently lives as a POSIX shell script
(`~/Projects/dotfiles/home/Util/motree.sh`, aliased `mt` in `.zshrc`). This
rewrite ports it to Go, in the (currently empty) `motree` repo at
`~/Projects/Personal/motree` (remote: `git@github.com-personal:mauricebutts/motree.git`),
for stability, readability, and extensibility — the bash version has grown
organically and leans on awk/sed positional-field parsing and a pane-text
heuristic that the original author already flagged as fragile.

Scope, as agreed: full behavioral parity with the bash version, plus one
deliberate improvement — replacing the status-detection heuristic with an
explicit Claude Code hook.

## Current behavior (source of truth: `home/Util/motree.sh`)

- **State**: `~/.motree`, a tab-separated flat file with two row kinds:
  - `SESSION <name> <description> <last_opened_unix_ts>`
  - `REPO <session_name> <repo_root> <worktree_path>`
  6 sessions are tracked in it right now (plus one orphaned `REPO` row with
  no matching `SESSION` row, already unreachable by the bash tool itself —
  see "Non-goals" below on why this doesn't need resolving yet).
- **`mt <name> [-b <base-branch>] [-d <description>]`**: run from inside a
  repo. Creates (or expands) a tmux session named `<name>`:
  - Worktree path: `<repo-root>/.claude/worktrees/<name>`.
  - Branch name: `<name>`. Base branch: `-b` flag, else repo's `origin/HEAD`,
    else `main`. New branches get `git branch --unset-upstream` after
    creation off `origin/<base>`.
  - New window per repo, named after the repo's basename, 3 panes: `nvim .`
    (left, full height), shell (top right), `claude` or `claude -c` (bottom
    right) — right column is 25% width, split top/bottom. `claude -c` is
    used iff `~/.claude/projects/<cwd with `.`/`/`/`_` → `-`>` already has
    transcript files.
  - Re-running for a repo already in the session is a no-op.
  - Attaches (`switch-client` if already inside tmux, else `attach-session`).
- **`mt spawn <name> "<prompt>" [-b][-d]`**: same creation path, but only for
  a brand-new session (errors if `<name>` already exists), sends `<prompt>`
  into the claude pane immediately via `send-keys`, and does not attach.
- **`mt pr <pr_number>`** (not yet committed to the dotfiles repo — found
  only in the deployed `~/Util/motree.sh`, ahead of the repo): checks out a
  PR into its own session for review.
  - Requires a numeric `<pr_number>` and the `gh` CLI; errors otherwise.
  - Session/worktree name: `pr-<pr_number>`. Worktree path:
    `<repo-root>/.claude/worktrees/pr-<pr_number>`.
  - If the worktree doesn't exist yet: resolves the PR's head branch via
    `gh pr view <pr_number> --json headRefName --jq .headRefName`. If that
    comes back empty, prints `no pr found` and exits 1 — no worktree,
    session, or state row gets created. Otherwise: `git worktree prune`,
    `git worktree add --detach <worktree_path>`, then `gh pr checkout
    <pr_number>` run inside that worktree (so `gh` manages the local branch
    and its tracking itself — no `-b <base-branch>` resolution, no
    `--unset-upstream` step, unlike the main `mt <name>` path).
  - State: `SESSION pr-<pr_number>` with description `PR #<pr_number>`, same
    `REPO` row tracking as any other session.
  - Same tmux session/window reuse and 3-pane layout as `mt <name>`, except
    the claude pane is launched straight into a fixed prompt ("Please give a
    high level explanation of this PR and it's business purpose") the same
    way `mt spawn` injects its prompt — but unlike `spawn`, `mt pr` still
    attaches at the end.
- **`mt ls`**: fzf picker over tracked sessions, sorted most- to
  least-recently-opened.
  - Row label: `<status-emoji> <name> | <last-opened date> (<repo,repo,...>) — <description>`.
  - Status emoji, from inspecting the last 5 lines of the claude pane's
    captured text: 🔔 waiting (`Esc to cancel`), 🏃 running (`esc to
    interrupt`), 🤖 subagents (`↓ to manage`), 🛑 stopped (claude process
    found, no marker matched), blank if no claude pane found at all.
  - Preview pane: live-redraws every 0.5s via a loop capturing the claude
    pane's last 28 lines (only works while fzf has a `--listen` socket to
    poll through, when `fzf` supports `--listen` and `curl` is present).
  - Picking a row attaches if the tmux session is live, or recreates it
    (worktree already exists, just rebuild the tmux session/windows/panes)
    if it isn't, pruning any repo rows whose worktree directory is gone.
- **`mt rm`**: fzf picker (no live preview, just the description), removes on
  selection: `git worktree remove` for every repo row, aborting the whole
  operation with the git error text if any removal fails (no partial
  removal), `git branch -d <name>` (best-effort, ignored if it fails),
  `tmux kill-session`, and prunes the SESSION/REPO rows from state.
- **`mt help`/`-h`/`--help`**: static usage text.
- Internal helpers of note: `mt_find_session_id` (enumerates tmux sessions
  because bare names collide with tmux's pane-separator parsing when a name
  contains `.`), `mt_quote` (POSIX single-quote escaping for `send-keys`).

## Goals

- Byte-for-byte compatible CLI surface and behavior described above:
  same subcommands, same flags, same worktree/branch mechanics, same tmux
  window/pane layout, same `mt ls`/`mt rm` interaction model, same resume
  logic (`claude` vs `claude -c`), same no-partial-removal semantics on `mt
  rm` failure, and `mt pr <pr_number>` ported as described above (including
  the `no pr found` failure message and the lack of `-b`/`-d` flags).
- State storage moves to a single JSON file. The path is configurable via
  an environment variable (suggested name `MOTREE_STATE_FILE`; builder may
  rename), **defaulting to `~/.motree`** so the eventual real tool matches
  today's location. For this build and all development/testing, that
  variable is set to **`~/.motree-dev`** instead — a deliberately separate
  path so we don't touch the real state file or need to solve migration
  yet. This also means the eventual cutover is just unsetting the env var
  (or pointing it at `~/.motree`), not a code change. Atomic writes (temp
  file + rename), same spirit as the current `mktemp`+`mv` pattern, so a
  crash or concurrent `mt` invocation can't corrupt it.
- **New: status detection via a Claude Code hook, replacing pane-text
  scraping.**
  - The `mt` binary gets a hidden subcommand (e.g. `mt __hook`) meant to be
    invoked by Claude Code's hook mechanism, not by hand.
  - Hook registrations are added to the existing hooks block in the
    **global** `~/.claude/settings.json` (confirmed with the user — `mt`
    operates against arbitrary repos with no fixed project config, so this
    can't be scoped per-project). The file already has entries wired for
    every relevant event for iTerm2's own `cc-status` binary
    (`Notification`, `PermissionRequest`, `PostToolUse`, `PreToolUse`,
    `SessionEnd`, `SessionStart`, `Stop`, `StopFailure`, `SubagentStop`,
    `UserPromptSubmit`) — the merge must be additive and must not disturb
    those existing entries.
  - Proposed event → status mapping (verify against real hook payload
    timing before finalizing — see "what's left to the builder"):
    `UserPromptSubmit`/`PreToolUse` → running, `Notification`/
    `PermissionRequest` → waiting, `SubagentStop` → subagents, `Stop`/
    `StopFailure`/`SessionEnd` → stopped.
  - Each firing writes one status atomically to a file keyed by the
    session's cwd, using the same escaping convention already used for
    `~/.claude/projects/<cwd-escaped>` (replace `.`, `/`, `_` with `-`).
  - `mt ls`/`mt rm`'s status-emoji classification reads this file directly
    instead of `tmux capture-pane` + grep. The live preview pane's raw
    content (the actual scrolling pane text shown under the picker) is
    unaffected — only the emoji/status classification changes source.
  - We looked at reusing iTerm2's own `cc-status` for this and ruled it
    out: it's a compiled binary bundled inside the iTerm2 app bundle, not a
    readable or extensible script.

## Non-goals (carried over from the original design)

- No repo registry/config file — `mt` always operates on the repo it's run
  from (`git rev-parse --show-toplevel`).
- No cross-repo Claude context sharing — each repo's `claude` pane stays an
  independent process.
- **No migration of the real `~/.motree` in this build.** With
  `MOTREE_STATE_FILE=~/.motree-dev` set during development, the new binary
  reads/writes only that file, starting empty. Cutting over — unsetting the
  env var (or repointing it at `~/.motree`), writing a one-time migration
  from the bash version's `~/.motree` (including deciding what happens to
  the orphaned `REPO` row with no matching `SESSION`), and repointing the
  `.zshrc` alias for real — is a deliberate later step, once the Go version
  has been used and trusted, not part of this build.

## Anti-goals

- Touching the real `~/.motree` or the `.zshrc` `alias mt=` during this
  build — both stay untouched (dev work runs against `~/.motree-dev` via
  `MOTREE_STATE_FILE`) until a deliberate later cutover (see Non-goals
  above).
- Clobbering the existing iTerm2 hook entries in global `settings.json`.
- Silently swallowing errors the bash version surfaces today — e.g. `mt rm`
  must still print the actual git error and abort the whole operation if a
  worktree won't remove cleanly, not just log-and-continue.

## Constraints

- Still shells out to `git`, `tmux`, `fzf`, `nvim`, `claude`, `curl`
  (for fzf's `--listen` live-reload socket), and now `gh` (for `mt pr`) —
  these are external tool integrations to invoke, not things to reimplement.
- Go 1.27 is already installed locally. Module path should match the
  existing GitHub remote (`github.com/mauricebutts/motree`).

## Approaches considered

**State storage**
- Flat tab-separated file (status quo format) — zero migration risk, but
  keeps exactly the positional-field parsing fragility this rewrite exists
  to fix.
- **JSON, single file (chosen)** — typed Go structs instead of positional
  awk fields, still a single flat file that's easy to inspect/back up, and
  the one-time migration of the 7 existing sessions is straightforward.
- SQLite — real concurrency guarantees, but adds a dependency and loses
  casual inspectability for what's currently a 13-line file. Overkill at
  this scale.

**Status detection**
- Keep scraping tmux pane text for Claude Code's prompt-footer strings
  (status quo) — simplest to port 1:1, but it's a heuristic tied to
  Claude Code's current UI wording; the original script's own comment
  already flags it as liable to silently break on a Claude Code update.
- **Claude Code hook writing an explicit status file (chosen)** — the
  sturdier version the original author's TODO already names. Hook payloads
  are structured data, not scraped UI text, so it survives Claude Code UI
  changes. Costs one global `settings.json` change (approved) and a small
  hidden hook subcommand in the binary.
- Reuse iTerm2's `cc-status` — investigated and ruled out (opaque compiled
  binary, no extension point).

## What's left to the builder

- Exact JSON schema for the state file, and the path/layout for the per-cwd
  status files — must not collide with each other, should live somewhere
  sensible relative to the (possibly dev-overridden) state file path.
- Final name for the state-file env var (suggested `MOTREE_STATE_FILE`) and
  whether the per-cwd status files derive their location from it (e.g.
  sibling to the state file) or get their own separate env var — either is
  fine as long as dev/prod stay cleanly separated the same way the state
  file does.
- Verify the proposed hook→status mapping against real Claude Code hook
  payloads/timing, particularly whether `PostToolUse` should read as
  "running" or needs its own transient state, before finalizing it.
- How the `~/.claude/settings.json` hook entries get installed — automatic
  on first run of the new binary vs. an explicit `mt install-hooks`
  command. Either is fine; pick whichever is simplest to implement
  correctly without disturbing the existing iTerm2 entries.
- CLI framework choice (stdlib `flag`-based subcommand dispatch vs. a
  library such as `cobra`) — either is fine; optimize for low dependency
  count and readability over features neither this tool nor its one user
  needs.
- Test strategy: given the heavy reliance on real `tmux`/`git`/`fzf`
  processes, decide the right mix of pure unit tests (state parsing,
  label formatting, base-branch resolution) vs. integration tests that
  actually drive `tmux`/`git` in a temp environment.
- Packaging/build instructions (plain `go build` vs. a `Makefile`) for this
  build. The real `~/.motree` migration and `.zshrc` alias cutover are
  explicitly out of scope — see Non-goals/Anti-goals — and don't need a
  plan yet.
