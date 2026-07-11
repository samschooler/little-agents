package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Task config store: ~/.local/state/little-agents/tasks/<name>.json
//
// A command task's shell command and failure policy live here (not in the
// crontab line) so arbitrary commands and multi-line handler prompts stay out
// of the crontab, dodging nested-quote problems. The crontab line only carries
// `--launcher command`.
// ---------------------------------------------------------------------------

type RetryPolicy struct {
	Count          int `json:"count"`           // extra re-runs after the first attempt
	BackoffSeconds int `json:"backoff_seconds"` // fixed delay between attempts
}

type HandlerPolicy struct {
	Enabled    bool   `json:"enabled"`
	Launcher   string `json:"launcher"`    // claude | codex (default claude)
	PromptFile string `json:"prompt_file"` // custom prompt; "" => built-in default
}

type FailurePolicy struct {
	Retry   *RetryPolicy   `json:"retry,omitempty"`
	Handler *HandlerPolicy `json:"handler,omitempty"`
}

type TaskConfig struct {
	Command   string        `json:"command"`
	OnFailure FailurePolicy `json:"on_failure"`
}

func tasksDir() string { return filepath.Join(stateDir(), "tasks") }

func taskConfigPath(name string) string { return filepath.Join(tasksDir(), name+".json") }

func loadTaskConfig(name string) (*TaskConfig, error) {
	return loadTaskConfigFrom(taskConfigPath(name), name)
}

// loadTaskConfigFrom reads a task config from an explicit path. record-result
// uses this with a path derived from the run pointer's own location, so it
// never depends on the ambient XDG environment (which may differ between the
// launcher and the tmux session the wrapper runs in).
func loadTaskConfigFrom(path, name string) (*TaskConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read task config for %q: %w", name, err)
	}
	var cfg TaskConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid task config for %q: %w", name, err)
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("task config for %q has an empty command", name)
	}
	return &cfg, nil
}

func saveTaskConfig(name string, cfg *TaskConfig) error {
	if err := os.MkdirAll(tasksDir(), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(taskConfigPath(name), data, 0644)
}

// ---------------------------------------------------------------------------
// Run pointer helpers
// ---------------------------------------------------------------------------

func saveRunPointer(path string, ptr RunPointer) error {
	data, err := json.MarshalIndent(ptr, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func loadRunPointer(path string) (*RunPointer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ptr RunPointer
	if err := json.Unmarshal(data, &ptr); err != nil {
		return nil, err
	}
	return &ptr, nil
}

// lilaBin returns the resolved path to this executable, for the wrapper and
// handler to call back into (record-result).
func lilaBin() string {
	exe, err := os.Executable()
	if err != nil {
		return "lila"
	}
	if r, e := filepath.EvalSymlinks(exe); e == nil {
		return r
	}
	return exe
}

func sessionNameFor(name, uuid string) string {
	p := uuid
	if len(p) > 8 {
		p = p[:8]
	}
	return fmt.Sprintf("sched-%s-%s", name, p)
}

// ---------------------------------------------------------------------------
// Command runner: run-scheduled --launcher command
// ---------------------------------------------------------------------------

func runCommandTask(name, cwd string, once bool) {
	cfg, err := loadTaskConfig(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run-scheduled: %v\n", err)
		os.Exit(1)
	}

	if once {
		removeScheduleFromCrontab(name)
	}

	if err := os.MkdirAll(runsDir(), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "run-scheduled: cannot create runs dir: %v\n", err)
		os.Exit(1)
	}

	uuid := newUUID()
	ts := time.Now().Format("20060102T150405")
	logPath := filepath.Join(runsDir(), fmt.Sprintf("%s-%s.log", name, ts))
	ptrPath := filepath.Join(runsDir(), fmt.Sprintf("%s-%s.json", name, ts))
	wrapPath := filepath.Join(runsDir(), fmt.Sprintf("%s-%s.sh", name, ts))

	ptr := RunPointer{
		UUID:    uuid,
		CWD:     cwd,
		Status:  "running",
		Kind:    "command",
		Command: cfg.Command,
		LogPath: logPath,
	}
	if err := saveRunPointer(ptrPath, ptr); err != nil {
		fmt.Fprintf(os.Stderr, "run-scheduled: cannot write run pointer: %v\n", err)
		os.Exit(1)
	}

	maxAttempts := 1
	delay := 0
	if cfg.OnFailure.Retry != nil && cfg.OnFailure.Retry.Count > 0 {
		maxAttempts += cfg.OnFailure.Retry.Count
		delay = cfg.OnFailure.Retry.BackoffSeconds
	}

	wrapper := generateWrapper(cfg.Command, logPath, lilaBin(), ptrPath, maxAttempts, delay)
	if err := os.WriteFile(wrapPath, []byte(wrapper), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "run-scheduled: cannot write wrapper: %v\n", err)
		os.Exit(1)
	}

	sessionName := sessionNameFor(name, uuid)
	if err := tmuxNewSession(sessionName, cwd, "bash "+shellQuote(wrapPath)); err != nil {
		fmt.Fprintf(os.Stderr, "run-scheduled: cannot create tmux session: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("run-scheduled: launched command task %s (uuid: %s)\n", sessionName, uuid)
}

// generateWrapper builds the bomb-proof wrapper script. It runs the command
// (in the tmux session's cwd), tees combined output to both the tmux pane (for
// realtime attach) and the log file (for history), captures the command's REAL
// exit code via PIPESTATUS (so tee's success can't mask a failure), handles the
// retry loop internally, and reports the final result back to lila as its last
// act. record-result runs INSIDE the session, so the daemon reaper never races
// it — the session is still alive until the result is recorded.
func generateWrapper(command, logPath, lila, ptrPath string, maxAttempts, delay int) string {
	q := shellQuote
	return fmt.Sprintf(`#!/usr/bin/env bash
set -u
LOG=%s
LILA=%s
PTR=%s
MAX=%d
DELAY=%d
attempts=0
code=0
echo "[lila] command task starting $(date -Iseconds)" | tee -a "$LOG"
while : ; do
  attempts=$((attempts+1))
  echo "[lila] --- attempt $attempts/$MAX ---" | tee -a "$LOG"
  { %s ; } 2>&1 | tee -a "$LOG"
  code=${PIPESTATUS[0]}
  if [ "$code" -eq 0 ]; then break; fi
  if [ "$attempts" -ge "$MAX" ]; then break; fi
  echo "[lila] attempt failed (code=$code); retrying in ${DELAY}s" | tee -a "$LOG"
  sleep "$DELAY"
done
echo "[lila] final code=$code after $attempts attempt(s) $(date -Iseconds)" | tee -a "$LOG"
"$LILA" record-result --pointer "$PTR" --code "$code" --attempts "$attempts" >>"$LOG" 2>&1
`, q(logPath), q(lila), q(ptrPath), maxAttempts, delay, command)
}

// ---------------------------------------------------------------------------
// record-result: the policy brain (Go, where judgment belongs)
// ---------------------------------------------------------------------------

func recordResultCmd(args []string) {
	var ptrPath string
	code := 0
	attempts := 1
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--pointer":
			if i+1 < len(args) {
				i++
				ptrPath = args[i]
			}
		case "--code":
			if i+1 < len(args) {
				i++
				code, _ = strconv.Atoi(args[i])
			}
		case "--attempts":
			if i+1 < len(args) {
				i++
				attempts, _ = strconv.Atoi(args[i])
			}
		}
	}
	if ptrPath == "" {
		fmt.Fprintln(os.Stderr, "record-result: --pointer is required")
		os.Exit(1)
	}
	applyResult(ptrPath, code, attempts, false)
}

// applyResult is the single place failure policy is decided. It is called both
// by the wrapper's record-result (normal path) and by the daemon reaper
// (crash path). It is idempotent: it only acts while the run is still
// "running", so a run can never be double-handled.
func applyResult(ptrPath string, code, attempts int, crashed bool) {
	ptr, err := loadRunPointer(ptrPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "record-result: cannot read pointer: %v\n", err)
		return
	}
	if ptr.Status != "running" {
		return // already handled — idempotent guard
	}

	c := code
	ptr.ExitCode = &c
	ptr.Attempts = attempts
	ptr.FinishedAt = time.Now().Format(time.RFC3339)

	if code == 0 && !crashed {
		ptr.Status = "success"
		saveRunPointer(ptrPath, *ptr)
		return
	}

	if crashed {
		ptr.Status = "crashed"
	} else {
		ptr.Status = "failed"
	}
	saveRunPointer(ptrPath, *ptr)

	// Handler dispatch only for command runs — never for handler runs
	// (no-recursion guard) and never for plain agent runs.
	if ptr.Kind != "command" {
		return
	}

	// Resolve the state layout from the pointer's own location, not from the
	// environment: <stateDir>/runs/<ptr>.json -> tasks/<name>.json alongside.
	runs := filepath.Dir(ptrPath)
	name := runNameFromFilename(filepath.Base(ptrPath))
	cfgPath := filepath.Join(filepath.Dir(runs), "tasks", name+".json")
	cfg, err := loadTaskConfigFrom(cfgPath, name)
	if err != nil || cfg.OnFailure.Handler == nil || !cfg.OnFailure.Handler.Enabled {
		return // log-only
	}

	handlerUUID, err := dispatchHandler(name, ptr, cfg, runs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "record-result: handler dispatch failed: %v\n", err)
		return
	}
	ptr.HandlerUUID = handlerUUID
	saveRunPointer(ptrPath, *ptr)
}

// ---------------------------------------------------------------------------
// Handler dispatch: reuse the existing agent-launch path with a synthesized
// prompt = (custom prompt file OR built-in default) + failure context block.
// ---------------------------------------------------------------------------

const defaultHandlerPrompt = `You are an automated failure handler for a scheduled command run headless via
lila (no human is watching). A scheduled shell command just failed. Be decisive
and finish autonomously.

Your job:
1. Read the captured output below and inspect the command, its cwd, and any
   scripts/files it invokes.
2. Diagnose the root cause.
3. If you can SAFELY resolve the cause (e.g. a missing directory, a stale lock,
   a small code bug in a script under this project), fix it, then RE-RUN the
   exact command yourself to verify it now exits 0 (ground truth — not your
   own judgement).
4. If you cannot safely fix it (credentials, external outage, anything risky),
   change nothing and clearly explain what is blocking.

End with a concise plain-text summary: root cause, the exact change you made
(or why none), and the verification result.`

func dispatchHandler(name string, cmdPtr *RunPointer, cfg *TaskConfig, runs string) (string, error) {
	launcher := "claude"
	if cfg.OnFailure.Handler.Launcher != "" {
		launcher = cfg.OnFailure.Handler.Launcher
	}

	base := defaultHandlerPrompt
	if pf := cfg.OnFailure.Handler.PromptFile; pf != "" {
		b, err := os.ReadFile(pf)
		if err != nil {
			return "", fmt.Errorf("cannot read handler prompt file %q: %w", pf, err)
		}
		base = string(b)
	}

	exitStr := "unknown"
	if cmdPtr.ExitCode != nil {
		exitStr = strconv.Itoa(*cmdPtr.ExitCode)
	}
	ctx := fmt.Sprintf(`

---
## Failure context (auto-generated by lila)
- command:   %s
- cwd:       %s
- exit code: %s (after %d attempt(s))
- log file:  %s

### Captured output (tail)
%s
`, cmdPtr.Command, cmdPtr.CWD, exitStr, cmdPtr.Attempts, cmdPtr.LogPath,
		fenced(tailFile(cmdPtr.LogPath, 8000)))
	full := base + ctx

	handlerUUID := newUUID()
	promptFile := filepath.Join(runs, fmt.Sprintf("%s-%s.handler-prompt.txt", name, handlerUUID[:8]))
	if err := os.WriteFile(promptFile, []byte(full), 0644); err != nil {
		return "", err
	}

	// Handler gets its own linked run pointer so history renders the chain:
	// command run (failed) -> handler run. Bump the timestamp until the path is
	// free so an instantly-failing command can't make the handler pointer
	// clobber the (same-second) command pointer.
	t := time.Now()
	var hPtrPath string
	for {
		hPtrPath = filepath.Join(runs, fmt.Sprintf("%s-%s.json", name, t.Format("20060102T150405")))
		if _, err := os.Stat(hPtrPath); os.IsNotExist(err) {
			break
		}
		t = t.Add(time.Second)
	}
	// A handler is an agent run; its transcript is resolved by UUID like any
	// other agent run (findSessionLog), so don't overload LogPath with the
	// prompt file — it stays on disk for debugging but isn't the run's "log".
	saveRunPointer(hPtrPath, RunPointer{
		UUID:       handlerUUID,
		CWD:        cmdPtr.CWD,
		Status:     "running",
		Kind:       "handler",
		ParentUUID: cmdPtr.UUID,
	})

	sessionName := sessionNameFor(name, handlerUUID)
	// Run the agent headless in print mode; when it exits, record its result so
	// the handler run leaves "running". kind=="handler" means applyResult won't
	// recurse into another handler.
	shellCmd := fmt.Sprintf("cat %s | %s --dangerously-skip-permissions --session-id %s -p ; %s record-result --pointer %s --code $? --attempts 1",
		shellQuote(promptFile), launcher, handlerUUID, shellQuote(lilaBin()), shellQuote(hPtrPath))
	if err := tmuxNewSession(sessionName, cmdPtr.CWD, shellCmd); err != nil {
		return "", fmt.Errorf("cannot create handler tmux session: %w", err)
	}
	return handlerUUID, nil
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func tailFile(path string, max int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "(no output captured)"
	}
	if len(data) > max {
		data = data[len(data)-max:]
	}
	return string(data)
}

func fenced(s string) string {
	return "```\n" + strings.TrimRight(s, "\n") + "\n```"
}

// reapCommandRun is called by the daemon when a command/handler session has
// vanished while its pointer still says "running" (wrapper killed, OOM, reboot
// mid-run). It marks the run crashed and applies the same failure policy, so a
// hard-killed job can still reach its handler and never silently disappears.
func reapCommandRun(ptrPath string, attempts int) {
	applyResult(ptrPath, -1, attempts, true)
}
