# Agent Scheduling Design

## Overview

Add the ability to schedule Claude Code / Codex agents to run once or on a recurring schedule. Scheduled agents launch autonomously via cron, do their work, and either exit or stay open for review.

## State Model

Three artifacts, no database:

1. **Crontab** -- source of truth for schedule timing and configuration. Each schedule is a crontab entry with a marker comment.
2. **Prompt file** -- `~/.local/state/little-agents/prompts/<name>.txt` stores the prompt content.
3. **Run pointer file** -- `~/.local/state/little-agents/runs/<name>-<timestamp>.json` stores the Claude session UUID and project path for resolving logs.

### Schedule Name Validation

Names must match `[a-zA-Z0-9_-]+`. Creating a schedule with a name that already exists is rejected with an error. This keeps names safe for use in file paths, crontab comments, and tmux session names.

### Crontab Entry Format

```
# lila:<name>
<cron-expression> bash -l -c '<lila-binary-path> run-scheduled --name <name> --cwd <path> --launcher <claude|codex> [--exit-on-finish] [--once]'
```

The binary path is resolved at schedule creation time via `os.Executable()` (matching the existing pattern in `daemon.go`), not hardcoded. The `bash -l -c` wrapper ensures the cron environment has the user's PATH and other variables needed by Claude Code and tmux.

**Disabled format:**

```
# lila:<name>
#DISABLED# <cron-expression> bash -l -c '<lila-binary-path> run-scheduled --name <name> --cwd <path> --launcher <claude|codex> [--exit-on-finish] [--once]'
```

The `#DISABLED#` prefix is unambiguous to parse and distinguishes disabled schedules from stray comments.

### Run Pointer File Format

```json
{"uuid": "<claude-session-uuid>", "cwd": "<working-directory>", "status": "running|exited|error"}
```

- `uuid` maps to Claude Code's JSONL session log.
- `cwd` is stored so the JSONL file can be located directly under `~/.claude/projects/<encoded-cwd>/` without walking the entire tree.
- `status` is updated by the daemon when it detects the tmux session has ended (see Run Status Detection).

## CLI Subcommands

```
lila schedule add <name>       Interactive setup: cron expression, cwd, launcher, exit-on-finish, prompt
lila schedule list             Parse crontab for lila: entries, display with human-readable cron descriptions
lila schedule rm <name>        Remove crontab entry, prompt file, and all run pointer files
lila schedule edit <name>      Re-prompt all fields, showing current values as defaults. Preserves enabled/disabled state and run history. Re-resolves binary path via os.Executable().
lila schedule runs <name>      List past runs: one line per run with timestamp, status, token count
lila schedule enable <name>    Remove #DISABLED# prefix from crontab command line
lila schedule disable <name>   Add #DISABLED# prefix to crontab command line (pause without deleting)
```

### Internal Command

```
lila run-scheduled --name <name> --cwd <path> --launcher <claude|codex> [--exit-on-finish] [--once]
```

This is what cron invokes. Not intended for direct user use.

## `run-scheduled` Execution Flow

1. Generate a UUID for the Claude session.
2. Read the prompt from `~/.local/state/little-agents/prompts/<name>.txt`.
3. If `--once` is set, immediately remove own crontab entry (self-cleanup, no daemon dependency).
4. Write the run pointer file to `~/.local/state/little-agents/runs/<name>-<timestamp>.json` with UUID, cwd, and status `"running"`.
5. Launch a tmux session named `sched-<name>-<uuid>`.
6. Build the command. The prompt is always read from the prompt file via stdin or tmux send-keys, never passed as a CLI argument (avoids shell injection and length limits):
   - Exit on finish: `tmux new-session -d -s <session-name> -c <cwd> "claude --dangerously-skip-permissions --session-id <uuid> -p < <prompt-file-path>"`
   - Stay open: Launch `claude --dangerously-skip-permissions --session-id <uuid>` in the tmux session, then send the prompt via `tmux send-keys`.
7. Exit. The tmux session runs independently.

## One-Shot Schedules

A one-shot schedule uses an empty cron field conceptually. In practice, the datetime (e.g., `2026-03-15T14:00:00`) is converted to a cron expression matching that specific minute/hour/day/month. The `--once` flag on `run-scheduled` causes it to remove its own crontab entry immediately after launching, preventing re-execution. Note: there is a narrow race window between cron firing and the entry being removed if the user is simultaneously editing the crontab. This is acceptable for expected usage patterns.

## Concurrency Policy

If a schedule's previous run is still active when the next cron trigger fires, a new session is launched. Parallel runs are allowed -- each gets its own UUID, tmux session, and pointer file. This is intentional: a long-running agent should not silently block the next scheduled run.

## Crontab Manipulation

All crontab operations follow the same pattern:

1. Read current crontab: `crontab -l`
2. Parse/modify the relevant `# lila:<name>` block (marker comment + command line)
3. Write back: `crontab -`

The `# lila:<name>` comment serves as a stable marker for locating, updating, and removing entries. This approach works identically on macOS and Linux.

## Cron Expression Display

Cron expressions are stored as-is. The TUI renders a human-readable description alongside each expression (e.g., `0 8 * * 1-5` displays as "Weekdays at 8:00 AM"). This is display-only; the stored value is always standard cron syntax.

## Run Status Detection

Run status is tracked in the pointer file's `status` field:

- **`running`** -- set by `run-scheduled` at launch. The tmux session `sched-<name>-<uuid>` is active.
- **`exited`** -- set by the daemon when it detects the tmux session has ended and the JSONL log exists with content.
- **`error`** -- set by the daemon when the tmux session has ended but the JSONL log is missing or empty (indicates the session failed to start or crashed).

The daemon already polls tmux sessions every 2 seconds. It can check for `sched-*` sessions transitioning from present to absent and update the corresponding pointer file.

## Token Count Per Run

Token counts displayed in the TUI are computed by summing usage entries from the run's JSONL session log -- the same parsing logic as `quota.go`'s `loadEntries` but filtered to a single session file (resolved via the pointer file's UUID and cwd). If the JSONL file cannot be found, the TUI displays "-" instead of a token count.

## TUI Changes

### Main Screen

The existing session list is unchanged. A new `[r]` keybinding navigates to the schedules screen. This avoids collision with `[s]` (install-service when daemon is not running), `[c]` (CLI toggle), `[n]` (new session), and `[k]` (kill session).

```
 Sessions
  a) . my-project     thinking (tool: edit)    ~/repo/myproject
  b) o api-work       waiting                  ~/repo/api

  [n]ew  [k]ill  [r]ecurring  [esc] quit
```

### Schedules Screen

Shows all scheduled agents and recent past runs with letter identifiers.

```
 Scheduled
  a) # daily-review      0 8 * * * (Daily at 8:00 AM)    ~/repo/myproject
  b) # weekly-cleanup    0 9 * * 1 (Mondays at 9:00 AM)  ~/repo/api
  c) # one-time-fix      0 14 15 3 * (once)               ~/repo/foo

 Past Runs
  d) + daily-review      2026-03-14 08:00    exited    1.2M tokens
  e) + daily-review      2026-03-13 08:00    exited    0.8M tokens
  f) x weekly-cleanup    2026-03-10 09:00    error     -

  [n]ew schedule  [esc] back
```

### Selection Mode

Pressing a letter identifier enters selection mode with a context-sensitive command palette. Escape deselects.

**Schedule selected:**
```
  [space] enable/disable  [d]elete  [enter] view details  [esc] deselect
```

**Past run selected:**
```
  [enter] view log  [d]elete log  [esc] deselect
```

### Schedule Detail View

Pressing enter on a selected schedule shows its configuration and full run history.

```
 daily-review
  Cron:      0 8 * * * (Daily at 8:00 AM)
  Dir:       ~/repo/myproject
  Launcher:  claude
  Exit:      yes
  Status:    enabled
  Prompt:    Review open PRs and summarize issues...

 Run History
  a) + 2026-03-14 08:00    exited    1.2M tokens
  b) + 2026-03-13 08:00    exited    0.8M tokens
  c) x 2026-03-12 08:00    error     -

  [space] enable/disable  [e]dit  [d]elete schedule  [enter] view log  [esc] back
```

The Prompt field shows the first line of the prompt file, truncated with "..." if longer. Pressing `[e]dit` enters the same interactive flow as `lila schedule edit <name>`.

## Log Resolution

When the user views a run log, the TUI:

1. Reads the pointer file to get the Claude session UUID and cwd.
2. Locates the JSONL file by scanning `~/.claude/projects/` for the directory matching the cwd and finding the session file with the matching UUID. The exact directory encoding scheme is determined at implementation time by inspecting Claude Code's actual directory structure.
3. Renders the session content.

This leverages Claude Code's existing logging rather than duplicating output capture.

## Daemon

One new responsibility: updating run pointer file status. When the daemon detects a `sched-*` tmux session has ended (already part of its polling loop), it updates the corresponding pointer file's `status` from `"running"` to `"exited"` or `"error"` based on whether the JSONL log exists. All other daemon behavior (unread detection, status tracking) works for scheduled sessions automatically since they are standard tmux sessions.

## Schedule Fields

Each schedule is defined by six fields:

| Field | Storage | Description |
|-------|---------|-------------|
| Name | Crontab marker comment | Unique identifier, must match `[a-zA-Z0-9_-]+` |
| Cron expression | Crontab schedule field | Standard cron syntax (or one-shot datetime converted to cron) |
| Working directory | `--cwd` flag in crontab | Repository path |
| Prompt | `prompts/<name>.txt` file | Task instructions |
| Launcher | `--launcher` flag in crontab | `claude` or `codex` |
| Exit on finish | `--exit-on-finish` flag in crontab | Whether to terminate after task completion |

## Platform Support

- **macOS**: crontab available natively.
- **Linux**: crontab available natively.
- No platform-specific codepaths needed (unlike the daemon's systemd/launchd split).
- Cron environment differences handled by wrapping commands with `bash -l -c` (see Cron Environment section).

## Files Changed

All changes are within `little-agents/go/`:

- **`main.go`** -- add `schedule` and `run-scheduled` command routing.
- **`schedule.go`** (new) -- crontab manipulation, schedule CRUD, `run-scheduled` execution logic.
- **`tui.go`** -- add schedules screen, schedule detail view, selection mode with command palettes.
- **`cron.go`** (new) -- cron expression parsing and human-readable description generation.
- **`daemon.go`** -- add run pointer status updates for `sched-*` session transitions.
