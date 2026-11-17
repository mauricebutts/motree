# motree (`mt`)

A personal CLI that orchestrates git worktrees and tmux sessions for doing
parallel coding-agent work across one or more repos (see
`docs/superpowers/specs/2026-11-14-motree-go-design.md` for the full design
and `docs/superpowers/plans/2026-11-15-motree-go.md` for the build plan).

## Setup

Requires Go (developed against 1.27), plus `git`, `tmux`, and `fzf` on
`PATH` (`gh` too if you want `mt pr`).

- **`fzf` must be >= 0.53.0.** `mt ls`/`mt cmd` render each session's
  subagent-status line as a second visual line of the same fzf item (not
  a separate row), which needs the multi-line item support fzf added in
  0.53.0. There is no fallback for older fzf — those rows will render
  incorrectly.

1. **Build it:**

   ```
   make build    # builds ./mt in the repo
   make test     # go test ./...
   ```

   To get `mt` onto `PATH`, use the conventional Go way instead of this
   Makefile:

   ```
   go install ./cmd/mt
   ```

   This drops the binary at `$(go env GOPATH)/bin/mt` — already on `PATH`
   for most Go setups, no further config needed. It never touches any
   existing `mt` alias in `.zshrc` — switching that alias over to this
   binary (or just removing it, since `PATH` would then resolve `mt` here
   directly) is a deliberate, separate step you take once you trust it,
   not something `go install` does automatically. Until then, invoke the
   binary explicitly (e.g. `\mt`, which bypasses a same-named shell alias
   and falls through to `PATH`).

2. **Point it at a state file, in your shell startup file — not ad-hoc per
   terminal:**

   ```
   echo 'export MOTREE_STATE_FILE=~/.motree-dev' >> ~/.zshrc
   ```

   then open a **new** terminal so it takes effect. `mt` tracks
   sessions/repos in a single JSON file; by default that's `~/.motree`.
   `MOTREE_STATE_FILE` lets you point it somewhere else instead — this
   project's own day-to-day
   development runs against `~/.motree-dev`, so it never touches your real
   session state while you're trying this out.

   **Why your shell startup file and not just `export` in one terminal:**
   the status-detection hook (next step) runs as a child process of
   whatever `claude` process spawned it — it inherits *that* process's
   environment, not the environment of whatever terminal you happen to run
   `mt ls` in. If `MOTREE_STATE_FILE` is only set in one terminal, `mt ls`
   there will look in `~/.motree-dev-status/` while the hook (launched from
   some other shell) writes to `~/.motree-status/` instead — so `mt ls`
   never sees a status, with no error anywhere to tell you why. Setting it
   in `.zshrc` means every new shell — and so every `claude` process
   launched from one — picks it up the same way.

   Remove the line (or point it at `~/.motree`) once you're ready for `mt`
   to manage your real session state.

3. **Run `mt install-hooks` once per machine:**

   ```
   mt install-hooks
   ```

   `mt ls`/`mt rm` show each session's live status (waiting / running /
   subagents / stopped) by reading a status file that Claude Code writes via
   a hook. This step registers `mt __hook` with Claude Code by merging an entry into
   your global `~/.claude/settings.json`, additively — any existing hook
   entries (e.g. for other tools) are left alone. Without this step, `mt ls`
   still works, just without the live status emoji.

## Usage

```
mt help
```

prints the full command list (`mt <name>`, `mt spawn`, `mt pr`, `mt ls`,
`mt rm`, `mt cmd`, `mt install-hooks`).
