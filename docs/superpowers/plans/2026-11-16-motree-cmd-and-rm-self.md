# mt cmd + mt rm --self Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port the two bash `motree.sh` features the Go rewrite is missing —
`mt rm --self` and the `mt cmd` command center — to this codebase, with
byte-for-byte-equivalent behavior.

**Architecture:** Add the handful of tmux primitives `mt cmd` needs
(display-message queries, a generic two-pane split, session-option
get/set, client listing/retargeting, window/pane selection, zoom
toggling, pane-pid liveness) to `internal/tmux`. Add a new
`internal/mtcmd/cmd.go` implementing `Dock`/`Quit`/`List`/`Cmd`, reusing
`RenderRows` and the existing `fzfutil` live-reload mechanism from `mt
ls`. Add `mt rm --self`'s confirmation flow to `internal/mtcmd/rm.go`.
Wire both into `cmd/mt/main.go`'s dispatch and help text.

**Tech Stack:** Go 1.27, stdlib only (plus the existing `tmux`/`fzf`/`git`
subprocess integrations already in this codebase).

**Spec:** `docs/superpowers/specs/2026-11-16-motree-cmd-and-rm-self.md`
(and the original `docs/superpowers/specs/2026-11-14-motree-go-design.md`
for overall conventions this plan doesn't repeat).

## Global Constraints

- Shells out to `git`, `tmux`, `fzf`, `gh` (already the case) plus
  `pgrep` (new, for `mt cmd`'s picker-liveness check).
- Reserved tmux session name for the command center: `__mt_cmd_center__`,
  window name `cmd`.
- No new persisted state: `@mt_cmd_docked` lives only as a tmux session
  option on `__mt_cmd_center__`.
- All new subprocess calls go through `executil.Runner` (via
  `tmux.Tmux.Runner`), tested with `executil.FakeRunner`, matching every
  existing file in `internal/tmux`.

## Review Focus

- `mt rm --self` run outside tmux (`$TMUX` unset) must print `mt: rm
  --self must be run from inside a tmux session` and exit 1, not panic on
  a missing current-session lookup.
- `mt rm --self` from a tmux session whose name isn't tracked must print
  `mt: current session '<name>' isn't tracked by mt` and exit 1 — not
  silently remove nothing or crash on a nil session lookup.
- `__mt_cmd_dock <name>` for a name with no live tmux session, or a live
  session with no running `claude` pane, must write its error via
  `display-message` into the command center's right pane and return
  success (no error, no process exit) — matching bash's `do_cmd_dock`,
  which never propagates these as command failures.
- Docking the same already-docked session twice in a row (e.g. picking
  the same fzf row again) must be a no-op re-select, not a second
  `tmux attach`/`switch-client` spawned into the right pane — covered by
  the client-tty-to-session-ID idempotency check.
- Docking a *different* session while one is already docked must
  `ZoomOff` the previous session's window before zooming the new one —
  otherwise two windows are left zoomed simultaneously, which is visibly
  wrong in tmux and diverges from bash's `do_cmd_dock`.

---

### Task 1: New tmux primitives for the command center

**Files:**
- Modify: `internal/tmux/tmux.go`
- Test: `internal/tmux/tmux_test.go`

**Interfaces:**
- Consumes: `executil.Runner` (existing), `executil.FakeRunner` (existing,
  for tests).
- Produces, all as methods on the existing `Tmux` struct:
  - `DisplayMessage(target, format string) (string, error)` — runs
    `tmux display-message -p -t <target> <format>` and returns trimmed
    stdout. Used for `#{pane_tty}`, `#{window_zoomed_flag}`, and (with
    `target=""`, see below) the untargeted `#S` query.
  - `DisplayMessageCurrent(format string) (string, error)` — runs
    `tmux display-message -p <format>` (no `-t`), for `rm --self`'s
    current-session-name lookup. Kept separate from `DisplayMessage`
    rather than accepting an empty target there, so an accidental empty
    target elsewhere is a visible bug, not a silently-valid untargeted
    query.
  - `ShowMessage(target, text string) error` — runs
    `tmux display-message -t <target> <text>` (no `-p`), for the two
    `do_cmd_dock` error messages into the right pane.
  - `SplitRightPane(windowTarget, cwd string, percent int) error` — runs
    `tmux split-window -h -p <percent> -t <windowTarget> -c <cwd>`.
    Generic (percent and cwd are parameters), unlike the existing
    hard-coded `SplitThreePane`.
  - `SessionOption(sessionID, name string) (string, error)` — runs
    `tmux show-option -t <sessionID> -v <name>` and returns trimmed
    stdout (empty string if unset — `show-option -v` on an unset option
    exits 0 with empty stdout, same as bash's usage here, which never
    distinguishes "unset" from an error via a separate check).
  - `SetSessionOption(sessionID, name, value string) error` — runs
    `tmux set-option -t <sessionID> <name> <value>`.
  - `ListClientSessions() (map[string]string, error)` — runs
    `tmux list-clients -F '#{client_tty} #{session_id}'` and returns a
    tty→sessionID map (empty map, not error, if no clients or tmux
    server isn't running — same "degrade to empty" convention
    `FindSessionID` already uses).
  - `SwitchClientTo(tty, sessionID string) error` — runs
    `tmux switch-client -c <tty> -t <sessionID>`.
  - `SelectWindow(target string) error` / `SelectPane(target string)
    error` — bare `tmux select-window -t <target>` /
    `tmux select-pane -t <target>`.
  - `PanePID(target string) (string, error)` — runs
    `tmux list-panes -t <target-window> -F '#{pane_index} #{pane_pid}'`
    and returns the pid for the pane index parsed out of `target`
    (`target` is `"<window>.<index>"`, same split convention
    `FindClaudePane`'s caller sites already use elsewhere in this
    codebase) — empty string, no error, if not found.
  - `HasChildProcess(pid string) bool` — runs `pgrep -P <pid>` via
    `t.Runner.Run` and returns whether it exited 0 (has at least one
    child). Never returns an error: a failed/erroring `pgrec` means "no
    child", same as bash's `pgrep ... >/dev/null 2>&1` only ever being
    checked for its exit code.
  - `ZoomOff(windowTarget string) error` / `ZoomOn(windowTarget string)
    error` — port `mt_zoom_off`/`mt_zoom_on` exactly: no-op (return nil)
    if `windowTarget == ""`; otherwise read
    `DisplayMessage(windowTarget, "#{window_zoomed_flag}")`, and only if
    it's `"1"` (`ZoomOff`) or `"0"` (`ZoomOn`) run
    `tmux resize-pane -Z -t <windowTarget>`. A query error is treated as
    "nothing to toggle" (return nil), matching bash's `|| return 0`.

- [ ] **Step 1: Write failing tests for each new method**

  One test per method above, following the existing
  `TestFindSessionID_*` style (`executil.FakeRunner` with a queued
  `Response`, assert both the returned value and the exact
  `fake.Calls` argv). At minimum:
  - `TestDisplayMessage_ReturnsTrimmedStdout`
  - `TestDisplayMessageCurrent_NoDashT`
  - `TestShowMessage_NoDashP`
  - `TestSplitRightPane_BuildsExpectedArgv`
  - `TestSessionOption_ReturnsEmptyStringWhenUnset`
  - `TestSetSessionOption_BuildsExpectedArgv`
  - `TestListClientSessions_ParsesTtySessionIDPairs`
  - `TestListClientSessions_EmptyOnNoClients` (fake returns `Err` — assert
    empty map, nil error)
  - `TestSwitchClientTo_BuildsExpectedArgv`
  - `TestSelectWindow_BuildsExpectedArgv` /
    `TestSelectPane_BuildsExpectedArgv`
  - `TestPanePID_ParsesMatchingIndex`
  - `TestHasChildProcess_TrueOnZeroExit` /
    `TestHasChildProcess_FalseOnError`
  - `TestZoomOff_TogglesOnlyWhenFlagIsOne`
  - `TestZoomOff_NoopWhenFlagIsZero`
  - `TestZoomOn_TogglesOnlyWhenFlagIsZero`
  - `TestZoomOff_NoopOnEmptyTarget` / `TestZoomOn_NoopOnEmptyTarget`
    (assert zero `fake.Calls`)

- [ ] **Step 2: Run tests to verify they fail**

  Run: `go test ./internal/tmux/... -run 'DisplayMessage|ShowMessage|SplitRightPane|SessionOption|SetSessionOption|ListClientSessions|SwitchClientTo|SelectWindow|SelectPane|PanePID|HasChildProcess|Zoom' -v`
  Expected: FAIL — undefined methods.

- [ ] **Step 3: Implement all methods listed above in `internal/tmux/tmux.go`**

  Follow the exact `tmux` argv shapes given in each bullet. Mirror the
  existing style in this file: each method is a thin wrapper around one
  `t.Runner.Run("tmux", ...)` call (or `"pgrep"` for `HasChildProcess`),
  with the same "degrade to empty/false rather than propagate a tmux
  error" convention `FindSessionID`/`FindClaudePane` already use where
  the spec calls for it (`ListClientSessions`, `HasChildProcess`, the
  zoom helpers' query step).

- [ ] **Step 4: Run tests to verify they pass**

  Run: `go test ./internal/tmux/... -v`
  Expected: PASS, all tests including pre-existing ones.

- [ ] **Step 5: Commit**

  ```bash
  git add internal/tmux/tmux.go internal/tmux/tmux_test.go
  git commit -m "feat(tmux): add primitives needed by mt cmd's command center"
  ```

---

### Task 2: `internal/mtcmd/cmd.go` — Dock, Quit, List, Cmd

**Files:**
- Create: `internal/mtcmd/cmd.go`
- Test: `internal/mtcmd/cmd_test.go`

**Interfaces:**
- Consumes: `Deps` (existing, `internal/mtcmd/deps.go`), `RenderRows`
  (existing, `internal/mtcmd/ls.go`), `fzfutil.SupportsListen`,
  `fzfutil.StartReloadPoller` (existing, `internal/fzfutil/fzfutil.go`),
  `fzfutil.Run` (existing — note its arg-building is specific to `mt
  ls`'s flags, so `List` builds its own argv rather than reusing
  `fzfutil.BuildArgs`), `d.Tmux.FindClaudePane`, `d.Tmux.FindSessionID`,
  and every new `tmux.Tmux` method from Task 1.
- Produces:
  - `const CmdSessionName = "__mt_cmd_center__"`
  - `WindowTargetFor(d *Deps, name string) (string, bool)` — ports
    `mt_window_target_for`: `""`, `false` if no live session for `name`
    or it has no repos; else `"<sessionID>:<basename of first repo's
    RepoPath>"`, `true`.
  - `Dock(d *Deps, mtBinaryPath, targetName string) error` — ports
    `do_cmd_dock`, per the spec's addendum section. Returns a non-nil
    error only for a genuine failure to talk to tmux at all (e.g.
    `DisplayMessage` erroring on the cmd session itself not existing);
    the "no running session for `<name>`" / "no claude pane" cases
    write via `ShowMessage` and return `nil`, matching bash's `do_cmd_dock
    ... || true` throughout.
  - `Quit(d *Deps) error` — ports `do_cmd_quit`.
  - `List(d *Deps, mtBinaryPath string) error` — ports `do_cmd_list`:
    builds its own fzf argv (`--delimiter='\t' --with-nth=2
    --height=100% --reverse --track --id-nth=1`, plus `--listen=<sock>`
    when supported, plus the `enter`/`ctrl-c` `--bind`s below, plus the
    `--header`), using `tmux.Quote` for every binary-path/socket
    interpolation into a `--bind` string (same escaping convention
    `fzfutil.BuildArgs` already uses for its `--preview`). `--bind`
    values:
    - `enter:execute-silent('<mtBinaryPath>' __mt_cmd_dock {1})`
    - `ctrl-c:execute-silent('<mtBinaryPath>' __mt_cmd_quit)+abort`
    - `--header='enter: show the selected claude pane on the right  ·  ctrl-c: quit command center'`
  - `Cmd(d *Deps, mtBinaryPath string, insideTmux bool) error` — ports
    `do_cmd`: find-or-create `CmdSessionName`'s `cmd` window (first run:
    `NewSession`+`SplitRightPane(..., os.Getenv("HOME"), 70)`+
    `SendKeys` into pane 0 with `<mtBinaryPath> __mt_cmd_list`; existing
    session: `PanePID` + `HasChildProcess` on pane 0, relaunch via the
    same `SendKeys` only if dead), then
    `d.Tmux.AttachOrSwitch(sessionID, insideTmux)`.

- [ ] **Step 1: Write failing tests for `WindowTargetFor`**

  ```go
  func TestWindowTargetFor_NoRepos_ReturnsFalse(t *testing.T)
  func TestWindowTargetFor_NoLiveSession_ReturnsFalse(t *testing.T)
  func TestWindowTargetFor_UsesFirstRepoBasename(t *testing.T)
  ```
  Build `d.State` with `state.State{Sessions: []state.Session{...}}`
  directly (no `Load`), fake `Tmux.Runner` queued for `FindSessionID`'s
  `list-sessions` call, assert the exact `"<id>:<basename>"` string.

- [ ] **Step 2: Run to verify failure, then implement `WindowTargetFor`, then run to verify pass**

  Run: `go test ./internal/mtcmd/... -run WindowTargetFor -v`

- [ ] **Step 3: Write failing tests for `Dock`**

  ```go
  func TestDock_NoLiveSession_WritesMessageAndReturnsNil(t *testing.T)
  func TestDock_NoClaudePane_WritesMessageAndReturnsNil(t *testing.T)
  func TestDock_AlreadyMirroring_OnlySelectsPane(t *testing.T)
  func TestDock_FirstDock_SendsAttachKeystroke(t *testing.T)
  func TestDock_RedockDifferentSession_ZoomsOffPreviousFirst(t *testing.T)
  func TestDock_ExistingMirrorClient_UsesSwitchClientNotSendKeys(t *testing.T)
  ```
  Each queues the exact sequence of fake responses Dock's tmux calls need
  (session/window lookups, `FindClaudePane`, `pane_tty` query,
  `list-clients`, `show-option`/`set-option`) and asserts on
  `fake.Calls` — in particular that `TestDock_AlreadyMirroring...` issues
  no `attach`/`switch-client` call at all, and that
  `TestDock_RedockDifferentSession...` calls `ZoomOff`'s
  `window_zoomed_flag` query for the *previous* window target before
  zooming the new one.

- [ ] **Step 4: Run to verify failure, then implement `Dock`, then run to verify pass**

  Run: `go test ./internal/mtcmd/... -run Dock -v`

- [ ] **Step 5: Write failing tests for `Quit`**

  ```go
  func TestQuit_NoSession_Noop(t *testing.T)
  func TestQuit_ZoomsOffDockedThenKillsSession(t *testing.T)
  func TestQuit_NoDockedOption_StillKillsSession(t *testing.T)
  ```

- [ ] **Step 6: Run to verify failure, then implement `Quit`, then run to verify pass**

  Run: `go test ./internal/mtcmd/... -run Quit -v`

- [ ] **Step 7: Write failing tests for `List`'s argv construction**

  Test the built fzf args directly (extract an unexported
  `cmdListFzfArgs(mtBinaryPath string, listen bool, socketPath string)
  []string` helper, same pattern `fzfutil.BuildArgs` uses, so this is
  testable without actually invoking fzf):
  ```go
  func TestCmdListFzfArgs_IncludesDockAndQuitBindings(t *testing.T)
  func TestCmdListFzfArgs_OmitsListenWhenUnsupported(t *testing.T)
  ```

- [ ] **Step 8: Run to verify failure, then implement `cmdListFzfArgs` and `List`, then run to verify pass**

  Run: `go test ./internal/mtcmd/... -run CmdListFzfArgs -v`

- [ ] **Step 9: Write failing tests for `Cmd`'s first-run vs. relaunch vs. leave-alone branches**

  ```go
  func TestCmd_FirstRun_CreatesSessionSplitsAndSendsPicker(t *testing.T)
  func TestCmd_ExistingSession_DeadPicker_Relaunches(t *testing.T)
  func TestCmd_ExistingSession_LivePicker_LeavesAlone(t *testing.T)
  ```
  "Dead"/"live" is driven by what `HasChildProcess` returns for the
  queued `PanePID` result.

- [ ] **Step 10: Run to verify failure, then implement `Cmd`, then run to verify pass**

  Run: `go test ./internal/mtcmd/... -run TestCmd_ -v`

- [ ] **Step 11: Run the full package test suite**

  Run: `go test ./internal/mtcmd/... -v`
  Expected: PASS, all tests including pre-existing ones.

- [ ] **Step 12: Commit**

  ```bash
  git add internal/mtcmd/cmd.go internal/mtcmd/cmd_test.go
  git commit -m "feat(mtcmd): port mt cmd's command center (dock/quit/list/cmd)"
  ```

---

### Task 3: `mt rm --self`

**Files:**
- Modify: `internal/mtcmd/rm.go`, `internal/state/state.go`
- Test: `internal/mtcmd/rm_test.go`, `internal/state/state_test.go`

**Interfaces:**
- Consumes: `tmux.Tmux.DisplayMessageCurrent` (Task 1),
  `mtcmd.RemoveSession` (existing).
- Produces:
  - `(s *State) IsTracked(name string) bool` in `internal/state/state.go`.
  - `RmSelf(d *Deps, stdin io.Reader, stdout io.Writer) error` in
    `internal/mtcmd/rm.go` — ports `do_rm`'s `--self` branch exactly:
    checks `os.Getenv("TMUX") == ""` → return the exact error
    `mt: rm --self must be run from inside a tmux session`; else resolve
    current session name via `d.Tmux.DisplayMessageCurrent("#S")`; if
    `!d.State.IsTracked(name)` → return the exact error
    `mt: current session '<name>' isn't tracked by mt`; else write the
    exact prompt `Remove session '<name>' and its worktree(s)? [y/N] `
    (no trailing newline) to `stdout`, read one line from `stdin` via
    `bufio.NewReader`, and on a `y`/`Y`-prefixed answer call
    `RemoveSession(d, name)`; otherwise print `mt: cancelled` to `stdout`
    and return `nil`. `stdin`/`stdout` are parameters (not
    `os.Stdin`/`os.Stdout` directly) so the test below can feed/capture
    them.

- [ ] **Step 1: Write failing tests for `IsTracked`**

  ```go
  func TestIsTracked_TrueForExistingSession(t *testing.T)
  func TestIsTracked_FalseForUnknownSession(t *testing.T)
  ```

- [ ] **Step 2: Run to verify failure, then implement `IsTracked`, then run to verify pass**

  Run: `go test ./internal/state/... -run IsTracked -v`

- [ ] **Step 3: Write failing tests for `RmSelf`**

  ```go
  func TestRmSelf_OutsideTmux_ReturnsExactError(t *testing.T)
  func TestRmSelf_UntrackedSession_ReturnsExactError(t *testing.T)
  func TestRmSelf_ConfirmedYes_RemovesSession(t *testing.T)
  func TestRmSelf_DeclinedAnswer_PrintsCancelledAndReturnsNil(t *testing.T)
  ```
  `TestRmSelf_OutsideTmux_ReturnsExactError` unsets `$TMUX` for the
  duration via `t.Setenv("TMUX", "")`. The other three set
  `t.Setenv("TMUX", "/tmp/fake")`, pre-populate `d.State` with a tracked
  session, and queue `DisplayMessageCurrent`'s fake response as that
  session's name; `_ConfirmedYes_` feeds `"y\n"` as stdin and asserts
  `RemoveSession`'s effects (session gone from state); `_DeclinedAnswer_`
  feeds `"n\n"` and asserts the state is unchanged and stdout contains
  `mt: cancelled`.

- [ ] **Step 4: Run to verify failure, then implement `RmSelf`, then run to verify pass**

  Run: `go test ./internal/mtcmd/... -run RmSelf -v`

- [ ] **Step 5: Commit**

  ```bash
  git add internal/mtcmd/rm.go internal/mtcmd/rm_test.go internal/state/state.go internal/state/state_test.go
  git commit -m "feat(mtcmd): port mt rm --self"
  ```

---

### Task 4: Wire `cmd/mt/main.go` dispatch and help text

**Files:**
- Modify: `cmd/mt/main.go`

**Interfaces:**
- Consumes: `mtcmd.Cmd`, `mtcmd.Dock`, `mtcmd.Quit`, `mtcmd.List`,
  `mtcmd.RmSelf` (Tasks 2–3), `newDeps` (existing).
- Produces: no new exported symbols — this task only adds dispatch cases
  and updates the two static text functions.

- [ ] **Step 1: Add dispatch cases**

  In the `switch os.Args[1]` block: add `case "cmd":` calling a new
  `runCmd() error` (resolves `mtBinaryPath`/`insideTmux` the same way
  `runLs` does, calls `mtcmd.Cmd`); `case "__mt_cmd_dock":` calling
  `runCmdDock(os.Args[2])` (guard `len(os.Args) < 3` the same way
  `__mt_preview` does); `case "__mt_cmd_list":` calling `runCmdList()`;
  `case "__mt_cmd_quit":` calling `runCmdQuit()`. In the `rm` case,
  change `runRm()` to `runRm(os.Args[2:])` and have it dispatch to
  `mtcmd.RmSelf(d, os.Stdin, os.Stdout)` when `len(args) > 0 &&
  args[0] == "--self"`, else the existing picker path — each error path
  printed via the existing `"mt: %v\n"` convention and `os.Exit(1)`,
  matching every other case in this switch.

- [ ] **Step 2: Update `usage()` and `printHelp()`**

  `usage()`: add `| mt cmd` to the trailing alternatives list, and
  restore the `[--self]` on `mt rm` that the existing comment at
  `cmd/mt/main.go:109` notes was dropped because it wasn't implemented
  yet — it now is.
  `printHelp()`: add the `mt cmd` section verbatim from the spec (copied
  from bash `do_help`, quoted in full in
  `docs/superpowers/specs/2026-11-16-motree-cmd-and-rm-self.md`), and a
  `mt rm --self` section under the existing `mt rm` one, also verbatim
  from that bash `do_help`.

- [ ] **Step 3: Build and smoke-test manually**

  Run: `go build -o /tmp/mt-smoke ./cmd/mt && /tmp/mt-smoke help`
  Expected: output includes both new sections, `usage()`'s one-liner
  (triggered by running `/tmp/mt-smoke` with no args) includes `mt cmd`
  and `mt rm [--self]`.

- [ ] **Step 4: Run the full test suite**

  Run: `go test ./...`
  Expected: PASS.

- [ ] **Step 5: Commit**

  ```bash
  git add cmd/mt/main.go
  git commit -m "feat(cli): wire up mt cmd and mt rm --self"
  ```

---

### Task 5: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the command list**

  `README.md`'s closing `mt help` paragraph lists `mt <name>`, `mt
  spawn`, `mt pr`, `mt ls`, `mt rm`, `mt install-hooks` — add `mt cmd`
  to that list (the printed help text is the source of truth; this is
  just keeping the README's own enumeration in sync with it).

- [ ] **Step 2: Commit**

  ```bash
  git add README.md
  git commit -m "docs: mention mt cmd in the command list"
  ```
