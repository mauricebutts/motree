# motree Go rewrite — `mt cmd` and `mt rm --self` addendum

## Intent

`docs/superpowers/specs/2026-11-14-motree-go-design.md` ported `~/Util/motree.sh`
to Go, but two features were added to the bash tool after that spec was
written (confirmed by diffing against `~/Util/motree.sh.bak-pre-spawn`,
the backup from just before `spawn`/`pr`/`help` — which the Go port
already has — were added):

- `mt rm --self`
- `mt cmd` — a "command center": a picker that stays open in a small tmux
  session and mirrors whichever tracked session's claude pane you pick,
  live, into a pane next to it.

This addendum documents both, same source-of-truth convention as the
original spec: `~/Util/motree.sh` (current, 823 lines), functions
`do_rm`'s `--self` branch, `mt_remove`'s trailing comment, `do_cmd`,
`do_cmd_dock`, `do_cmd_list`, `do_cmd_quit`, `mt_window_target_for`,
`mt_zoom_off`, `mt_zoom_on`.

Scope: behavioral parity with these two bash features only. Everything in
the original spec (state format, hook-based status, other subcommands)
is unchanged and out of scope here.

## Current behavior

### `mt rm --self`

- Only valid run from inside a tmux session (checks `$TMUX`); otherwise
  prints `mt: rm --self must be run from inside a tmux session` and exits 1.
- Resolves the current session's name via `tmux display-message -p '#S'`.
- Looks it up in tracked sessions; if untracked, prints
  `mt: current session '<name>' isn't tracked by mt` and exits 1.
- Prompts `Remove session '<name>' and its worktree(s)? [y/N] ` on stdout,
  reads one line from stdin. `[Yy]*` proceeds to the same removal path as
  picking your own session from `mt rm`'s picker (worktrees, branch,
  state, then tmux kill-session — in that order, kill-session last because
  it can SIGHUP the very process running this command). Anything else
  prints `mt: cancelled` and exits 0.

### `mt cmd`

- Reserved tmux session name `__mt_cmd_center__` — deliberately never a
  name a real worktree session would have (plain names like
  `cmd-center` have collided with a real tracked session on this user's
  machine before).
- **First run** (no `__mt_cmd_center__` session yet): creates it with one
  window named `cmd`, no fixed size. Splits that window horizontally,
  `-p 70` (new/right pane gets 70% width, left pane the remaining 30%),
  both panes starting in `$HOME`. Sends `<mt-binary> __mt_cmd_list` into
  the **left** pane (index 0) — that's the picker. The **right** pane
  (index 1) starts as a bare shell; nothing is docked into it yet.
- **Subsequent runs** (session already exists): checks whether the left
  pane's foreground job is still alive, via `pgrep -P <left pane pid>`
  (NOT `#{pane_current_command}` — confirmed empirically unreliable for a
  piped foreground job on this system). If the picker died (user hit
  Esc/Ctrl-C earlier, which exits fzf and leaves a bare shell), relaunches
  it the same way as the first-run case. If it's still alive, leaves it
  alone.
- Either way, attaches (`switch-client` if already inside tmux, else
  `attach-session`) to `__mt_cmd_center__` at the end.

- **`__mt_cmd_list`** (the left pane's picker): an fzf picker over
  `mt_render_rows` — same rows `mt ls` uses, live-reloaded on the same
  1-second `--listen` socket mechanism `mt ls` already uses (requires
  `fzf --listen` support; degrades to no live-reload otherwise, same as
  `mt ls`). No preview window.
  - `enter` → `execute-silent(<mt-binary> __mt_cmd_dock {1})` — dock the
    selected row's session into the right pane, without leaving fzf.
  - `ctrl-c` → `execute-silent(<mt-binary> __mt_cmd_quit)+abort` — tear
    down whatever's docked and kill the whole `__mt_cmd_center__` session,
    then exit fzf.
  - If no sessions are tracked, or `fzf` isn't installed, prints the same
    messages `mt ls`/`mt rm` do and returns (no picker shown).

- **`__mt_cmd_dock <name>`**: makes the right pane (`__mt_cmd_center__`'s
  `cmd` window, pane index 1) mirror `<name>`'s claude pane.
  - Resolves `<name>`'s **window target**: its tmux session ID, plus the
    window named after its *first* tracked repo's basename
    (`mt_window_target_for`). If `<name>` has no live tmux session or no
    repos, `tmux display-message`s `mt cmd: '<name>' has no running
    session` into the right pane and returns (no error) — doesn't touch
    anything else.
  - Within that window, finds the pane running `claude`
    (`mt_find_claude_pane`, same helper `mt ls`'s preview uses). If none,
    `display-message`s `mt cmd: no claude pane running for '<name>'` into
    the right pane and returns.
  - **Idempotency check**: reads the right pane's own tty
    (`#{pane_tty}`), then checks `tmux list-clients` for a client whose
    tty matches and whose attached session is already `<name>`'s session
    ID. If so, the right pane is already mirroring this exact session —
    just `select-pane` on it and return (no re-dock, no flicker).
  - **Un-dock the previous occupant**: reads the `@mt_cmd_docked` tmux
    session option off `__mt_cmd_center__`. If it names a *different*
    session than the one being docked now, un-zooms that previous
    session's claude-pane window (see zoom helpers below) — so only the
    currently-docked session's window stays zoomed.
  - **Dock the new one**: `select-window`/`select-pane` to the target's
    claude pane, then zooms that window (see below) — so the claude pane
    fills the whole window, which is what actually gets mirrored into the
    small right pane. Then:
    - If no existing client has that tty (first dock ever, or the mirror
      client died): sends `TMUX= tmux attach -t <target_session_id>` as a
      keystroke into the right pane — the `TMUX=` clears the env var so
      nested `tmux attach` doesn't refuse to run inside itself.
    - Else (a mirror client on that tty already exists, just pointed at
      a different session): `tmux switch-client -c <right_tty> -t
      <target_session_id>` — redirects the existing client instead of
      spawning a second nested attach.
  - Sets `@mt_cmd_docked` to `<name>` on `__mt_cmd_center__`, then
    `select-pane`s back to the right pane so the picker (left) keeps focus
    for the *next* fzf action, not the just-docked pane.
  - A docked session being killed and recreated under the same tracked
    name (new tmux session ID) must still redock cleanly on the next pick
    of that name — the idempotency check above is keyed on session ID via
    the live client list, not just the tracked name, specifically so this
    case is detected as "different" and redocks instead of no-op'ing.

- **`__mt_cmd_quit`**: reads `@mt_cmd_docked` off `__mt_cmd_center__` (if
  any), un-zooms that session's claude-pane window, then
  `tmux kill-session`s `__mt_cmd_center__` itself. No-ops if that session
  doesn't exist.

- **Zoom helpers** (`mt_zoom_off`/`mt_zoom_on`, both independently
  idempotent — safe to call regardless of current state, both no-op
  silently if `window_target` is empty or can't be queried):
  - `mt_zoom_off <window_target>`: reads `#{window_zoomed_flag}` for that
    window; if `"1"`, `tmux resize-pane -Z` (toggle off).
  - `mt_zoom_on <window_target>`: same read; if `"0"`, `tmux resize-pane
    -Z` (toggle on).

- `do_help`'s `mt cmd` section (verbatim, already quoted in the original
  bash `do_help`):

  ```
  mt cmd
      Open the command center: a live picker of tracked sessions on the
      left, and whichever one you pick's claude pane, live and interactive,
      on the right. Pick a different session to switch the right pane.
      Esc closes the picker but leaves the command center running (re-open
      with 'mt cmd'); Ctrl-C un-zooms whatever's docked and closes it for
      good.
  ```

## Goals

- Byte-for-byte-equivalent behavior for both features, as described above,
  including:
  - The exact reserved session name `__mt_cmd_center__` and window name
    `cmd`.
  - The exact pane layout (`-h -p 70`, both panes starting in `$HOME`).
  - The exact `@mt_cmd_docked` tmux-option bookkeeping and its two
    idempotency checks (tty→session-ID client match; previous-vs-new
    docked name).
  - The exact fzf bindings and messages for `__mt_cmd_list`.
  - The exact `rm --self` prompt text, stdin-read confirmation, and
    error/cancel messages.
- Reuse what the Go port already built for `mt ls` rather than
  reimplementing it: `RenderRows`, `fzfutil.SupportsListen` /
  `fzfutil.BuildArgs`-equivalent arg building / `fzfutil.StartReloadPoller`,
  `tmux.Tmux.FindClaudePane`.

## Non-goals

- No change to the reserved-name collision behavior beyond what bash
  already does (bash doesn't special-case a real collision on
  `__mt_cmd_center__` either — it's just named obscurely enough that one
  hasn't happened).
- No persistence of `@mt_cmd_docked` or command-center state in
  `~/.motree` (or its dev equivalent) — it lives purely as a tmux session
  option on `__mt_cmd_center__`, exactly as bash does it, and is gone once
  that session is killed.

## New primitives needed (not yet in `internal/tmux`)

The existing `tmux.Tmux` (see `internal/tmux/tmux.go`) has
`FindSessionID`, `NewSession`, `NewWindow`, `WindowExists`,
`SplitThreePane`, `SendKeys`, `FindClaudePane`, `CapturePane`,
`KillSession`, `AttachOrSwitch`. None of these cover:

- Querying an arbitrary `tmux display-message -p -t <target> '<fmt>'`
  (needed for `#{pane_tty}`, `#{window_zoomed_flag}`, and plain
  `display-message -t <pane> "<text>"` for the two error messages into the
  right pane).
- A two-pane horizontal split at an arbitrary percentage in an arbitrary
  cwd (bash's `split-window -h -p 70 -t <window> -c "$HOME"` — not the
  existing `SplitThreePane`, which is hard-coded to the 3-pane
  nvim/shell/claude layout).
- Reading/writing a tmux **session** option (`show-option -t <sid> -v
  <name>`, `set-option -t <sid> <name> <value>`).
- `tmux list-clients -F '#{client_tty} #{session_id}'`.
- `tmux switch-client -c <tty> -t <session_id>` (retargeting a specific
  client by tty, distinct from the existing `AttachOrSwitch`, which always
  means "this process's own controlling client").
- `tmux select-window -t <target>` / `tmux select-pane -t <target>`
  (bare, no `-Z`).
- The pid-liveness check for "is the left pane's picker still running":
  `tmux list-panes -t <window>.0 -F '#{pane_index} #{pane_pid}'` to get
  the pid, then `pgrep -P <pid>`. `pgrep` is a real `exec.Command` call
  outside tmux, same category as `git`/`fzf`/`gh` elsewhere in this
  codebase.
- `tmux display-message -p '#S'` with no `-t` (current session name) — the
  only fully-untargeted use of `display-message` in the tool, for
  `rm --self`.

## Constraints

Same as the original spec: shells out to `git`, `tmux`, `fzf`, `gh`
already; this addendum adds `pgrep` to that list (POSIX/macOS standard
utility, no new install requirement for this user's machine).

## What's left to the builder

- Exact Go method names/signatures for the new tmux primitives above —
  pick names consistent with the existing `tmux.Tmux` method style
  (verb-first, e.g. `DisplayMessage`, `SessionOption`, `SetSessionOption`,
  `ListClientSessions`, `SwitchClientTo`, `SelectWindow`, `SelectPane`,
  `SplitRightPane`, `CurrentSessionName`, `PanePID`).
- Whether `mt_zoom_off`/`mt_zoom_on`'s flag-check-then-toggle logic lives
  in `internal/tmux` (as `ZoomOff`/`ZoomOn` methods mirroring the bash
  helpers 1:1) or in the `mtcmd` layer on top of lower-level primitives —
  either is fine; the tmux-package-level option reads lower-level and is
  probably simpler to test.
- Where `rm --self`'s stdin confirmation prompt lives — likely alongside
  `mtcmd.Rm`/`RemoveSession` in `internal/mtcmd/rm.go`, reading via
  `bufio.Reader` over `os.Stdin` the same way the rest of this codebase
  would (there's no existing stdin-reading precedent to match yet; this
  is the first one).
- Test strategy for `mt cmd`'s tmux-heavy branches (first-run vs.
  relaunch-dead-picker vs. leave-alone; dock/re-dock/un-dock sequencing)
  — likely a fake `executil.Runner` recording calls, consistent with how
  `internal/tmux/tmux_test.go` already tests the rest of this package,
  rather than a real tmux process.
