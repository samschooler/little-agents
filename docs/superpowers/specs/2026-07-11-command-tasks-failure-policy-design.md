# Command Tasks & Failure Policy Design

## Overview

Extend lila from an agent launcher into a supervising cron. Today lila only launches `claude`/`codex`. This adds **command tasks**: any scheduled job runs *through* `lila run-scheduled` rather than as a raw crontab line. Because lila invokes the command, it observes the exit code and applies a per-schedule **failure policy** — record it, retry it, and/or hand it to an agent.

Every job — agent or command — then gets uniform run history, realtime + historical observability, and (for commands) automatic failure handling. Notifications are explicitly **not** part of lila (see Non-Goals).

## Goals

- Schedule arbitrary shell commands through the existing lila machinery.
- Make command execution **bomb-proof** (no silently-lost failures) and **observable both in realtime and in history**.
- On failure, apply a composable policy: always log; optionally retry; optionally dispatch an agent handler.
- Reuse the existing `claude`/`codex` launcher path for the handler — minimal new surface area.

## Non-Goals (v1)

- **Notifications.** A failed command (after retries and handler) simply lands in run history as `failed`. lila never emails, pushes, or pings. If a schedule wants alerting, its handler prompt can instruct the agent to use an email/notification skill *if one is available in that agent's environment*. A future generalized notifier may hook the seam left in the policy engine, but nothing is built for it now.
- **Failure policy for agent tasks.** Only command tasks get the failure policy in v1. Scheduled `claude`/`codex` tasks behave as they do today.
- **Rich retry strategies.** A fixed inter-attempt delay only; no exponential backoff, jitter, or circuit-breaking in v1.

## State Model

Consistent with the existing no-database model. A command task adds one artifact beyond the crontab entry and run pointers:

1. **Crontab** — source of truth for timing. The line stays dumb (see below).
2. **Task config file** — `~/.local/state/little-agents/tasks/<name>.json` — stores the command, and the failure policy. This mirrors how `prompts/<name>.txt` works for agent tasks and keeps arbitrary commands (and multi-line custom handler prompts) out of the crontab, dodging nested-quote problems.
3. **Run pointer file** — `~/.local/state/little-agents/runs/<name>-<timestamp>.json` — extended with command-task fields (exit code, attempts, finished-at, log path, handler linkage).
4. **Run log file** — `~/.local/state/little-agents/runs/<name>-<timestamp>.log` — captured stdout+stderr of the command, for realtime tailing and historical review.

### Crontab Entry Format

The crontab line carries no command text — only the launcher selector:

```
# lila:<name>
<cron-expression> bash -l -c '<lila-binary-path> run-scheduled --name <name> --cwd <path> --launcher command'
```

`--launcher command` is the new launcher value alongside `claude`/`codex`. When lila sees it, it loads the command and policy from the task config file rather than launching an agent. `--exit-on-finish`/`--once` remain available and orthogonal.

### Task Config File Format

`~/.local/state/little-agents/tasks/<name>.json`:

```jsonc
{
  "command": "restic backup /data",
  "on_failure": {
    "retry":   { "count": 2, "backoff_seconds": 30 },   // optional; omit or count:0 = no retry
    "handler": {                                          // optional; omit or enabled:false = log only
      "enabled": true,
      "launcher": "claude",                               // which agent CLI fixes it: claude | codex
      "prompt_file": null                                 // path to a custom prompt; null = built-in default
    }
  }
}
```

Absent `on_failure`, a command task is **log-only** — it records the failure and stops. This is the baseline default.

### Run Pointer File Format (extended)

Command-task pointers extend the existing `{uuid, cwd, status}` shape:

```jsonc
{
  "uuid": "<uuid>",
  "cwd": "<working-directory>",
  "status": "running | success | failed | crashed",
  "kind": "command",              // distinguishes from agent runs ("agent")
  "exit_code": 1,                 // command's real exit (via PIPESTATUS), once finished
  "attempts": 3,                  // total command invocations incl. retries
  "finished_at": "<timestamp>",
  "log_path": "<…>/runs/<name>-<ts>.log",
  "handler_uuid": "<uuid|null>"   // set on the failed run when a handler is dispatched
}
```

The handler's own run pointer carries `kind: "handler"` and a `parent_uuid` back-reference, so history renders the chain: command run `failed` → handler run spawned.

## Execution Model (bomb-proof + observable)

`run-scheduled --launcher command` branches to the command path:

1. Load the task config file; error out clearly if the command is missing/empty.
2. Write a run pointer (`status: running`, `kind: command`) and derive the `.log` path.
3. Launch a **generated wrapper script inside a tmux session** (reusing the existing tmux-session helper). The wrapper's responsibilities:
   - Run the command with `cwd` set.
   - `tee` combined stdout+stderr into the `.log` file.
   - Capture the **command's real exit code** via `${PIPESTATUS[0]}` (so `tee`'s success doesn't mask a failure).
   - Handle the **retry loop internally** — mechanical, self-contained, and independent of whether the lila daemon/binary is otherwise busy. Re-run up to `retry.count` times with `backoff_seconds` between attempts, stopping early on the first success.
   - On completion (success or final failure), call back: `lila record-result --pointer <path> --code <final-exit> --attempts <n>`.

Rationale for the split: **retry is mechanical**, so it lives in the sandboxed wrapper where it can't be interrupted; **handler dispatch is consequential/judgment-laden**, so it lives in Go (see next section).

**Realtime observability:** `tmux attach` to the live session, and the TUI can tail the `.log` file. **Historical observability:** the pointer records exit code / attempts / finished-at, and the `.log` persists.

## Failure Policy (in Go)

`lila record-result` is the policy brain. It runs *inside the tmux session* (as the wrapper's last act), whose environment may not match the launcher's — so it must **resolve the state layout from the run pointer's own path, not from `$XDG_STATE_HOME`**. Given `<stateDir>/runs/<name>-<ts>.json`, the task config is `<stateDir>/tasks/<name>.json` and handler artifacts go in `<stateDir>/runs/`, all derived by walking up from the pointer path. This keeps the result path bomb-proof regardless of the ambient environment. It updates the pointer with the final exit code, attempts, and finished-at, then:

- **Success** (exit 0): `status: success`. Done.
- **Final failure** (non-zero after all retries): `status: failed`, then:
  1. **log** — already recorded above (exit code, attempts, log path). Always happens.
  2. **handler** — if `on_failure.handler.enabled`: synthesize a prompt and dispatch an agent via the **existing launcher path** (`run-scheduled`'s agent branch, `--launcher` = `handler.launcher`), running in the same `cwd`. Record `handler_uuid` on the failed run and `parent_uuid` on the handler run.

### Handler Prompt

The dispatched agent receives: **(custom prompt file contents if `prompt_file` set, otherwise the built-in default)** followed by a **context block**:

```
A scheduled command failed.
  command:   <command>
  cwd:       <cwd>
  exit code: <n>  (after <attempts> attempt(s))
  output:    <tail of the .log, plus its full path>
```

**Built-in default prompt** instructs: *diagnose the failure → if you can safely resolve the cause, do so and re-run the command to verify → otherwise write a concise root-cause summary.* This is a default that may change state, chosen deliberately over report-only.

### Guards

- **No recursion.** Handler runs are agent tasks and never themselves trigger a failure handler, regardless of their own exit. This prevents fix-loops.
- Retry lives in the wrapper; handler dispatch lives in Go. Clean separation of mechanical vs. consequential.

## The Reaper (belt & suspenders)

The daemon already tracks tmux session liveness by UUID. Extended for command tasks: if a command session's tmux session has vanished while its pointer still says `running` (wrapper killed, OOM, box reboot mid-run), the daemon marks the pointer `crashed` and applies the same failure policy (so a hard-killed job can still reach its handler and can never silently disappear). The reaper must be idempotent — it must not double-dispatch a handler for a run that `record-result` already handled.

## Components & Boundaries

- **Task config store** (`tasks/<name>.json` read/write) — new; parallels the existing prompt-file helpers.
- **Command runner** (`run-scheduled --launcher command`) — generates the wrapper, opens the tmux session, writes the initial pointer.
- **`record-result` subcommand** — the policy engine; updates the pointer and dispatches the handler. The single seam where a future notifier would attach.
- **Handler dispatcher** — thin adapter that synthesizes the prompt + context block and calls the existing agent launcher path.
- **Reaper** — extension to the existing daemon poll loop.

## Testing

- Wrapper exit-code capture: command exits non-zero → pointer reflects it (assert `PIPESTATUS` handling, not `tee`'s).
- Retry loop: transient command (fails then succeeds) stops early with `status: success` and correct `attempts`; always-failing command exhausts retries then reports final failure.
- Policy: log-only config records `failed` and dispatches no handler; handler config dispatches an agent with the correct synthesized prompt + context and links `handler_uuid`/`parent_uuid`.
- No-recursion guard: a failing handler run does not spawn another handler.
- Reaper: killing a running command's tmux session leads to `crashed` + policy applied exactly once (idempotency).
- Config parsing: missing `on_failure` → log-only; missing/empty command → clear error.
