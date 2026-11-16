# motree (`mt`)

A personal CLI that orchestrates git worktrees and tmux sessions for doing
parallel coding-agent work across one or more repos. This is a Go port of
the original `~/Util/motree.sh` bash script, kept behavior-compatible (see
`docs/superpowers/specs/2026-11-14-motree-go-design.md` for the full design
and `docs/superpowers/plans/2026-11-15-motree-go.md` for the build plan).

## Build

Requires Go (developed against 1.27).

```
make build    # builds ./mt
make test     # go test ./...
make install  # builds, then copies ./mt to ~/Util/mt-go
```

`make install` never touches the existing bash tool (`~/Util/motree.sh`) or
the `mt` alias in `.zshrc` — switching the real `mt` alias over to this
binary is a deliberate, separate step you take once you trust it, not
something this build does automatically.

## State file: `MOTREE_STATE_FILE`

`mt` tracks sessions/repos in a single JSON file. By default that's
`~/.motree` — the same location (in spirit) the bash tool used.

Set `MOTREE_STATE_FILE` to point `mt` at a different file instead. This
project's own day-to-day development runs against `~/.motree-dev`, so it
never touches your real session state:

```
export MOTREE_STATE_FILE=~/.motree-dev
```

Unset (or unset in `.zshrc`) once you're ready for `mt` to manage your real
`~/.motree`.

## One-time setup: `mt install-hooks`

`mt ls`/`mt rm` show each session's live status (waiting / running /
subagents / stopped) by reading a status file that Claude Code writes via a
hook, rather than scraping pane text. Run this once per machine so Claude
Code knows to call `mt __hook`:

```
mt install-hooks
```

This merges an entry into your global `~/.claude/settings.json`,
additively — any existing hook entries (e.g. for other tools) are left
alone.

## Usage

```
mt help
```

prints the full command list (`mt <name>`, `mt spawn`, `mt pr`, `mt ls`,
`mt rm`, `mt install-hooks`).
