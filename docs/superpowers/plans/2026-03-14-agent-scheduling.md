# Agent Scheduling Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add one-shot and recurring agent scheduling via cron to Little Agents (lila).

**Architecture:** Crontab is the source of truth for schedule timing/config. Prompt files store task instructions. Run pointer files (tiny JSON with Claude session UUID) link scheduled runs to Claude Code's own JSONL logs. The TUI gets a new schedules screen with selection mode and schedule detail view.

**Tech Stack:** Go stdlib only (no new dependencies). Uses `os/exec` for crontab/tmux commands, `crypto/rand` for UUID generation, `encoding/json` for pointer files.

**Spec:** `docs/superpowers/specs/2026-03-14-agent-scheduling-design.md`

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `go/cron.go` | Create | Cron expression → human-readable description, datetime → cron conversion |
| `go/cron_test.go` | Create | Tests for cron utilities |
| `go/schedule.go` | Create | Schedule type, crontab parsing/building, name validation, prompt file I/O, run pointer I/O, crontab system operations, UUID generation, `run-scheduled` execution, session log resolution |
| `go/schedule_test.go` | Create | Tests for crontab parsing/building, name validation, pointer file parsing |
| `go/schedule_cli.go` | Create | Interactive CLI flows for schedule add/edit/list/rm/runs/enable/disable |
| `go/main.go` | Modify | Add `schedule` and `run-scheduled` command routing |
| `go/daemon.go` | Modify | Add sched-* session transition detection and pointer file status updates |
| `go/tui.go` | Modify | Add `[r]` keybinding, schedules screen, selection mode, detail view |

---

## Chunk 1: Foundation

### Task 1: Cron Expression Utilities

**Files:**
- Create: `go/cron.go`
- Create: `go/cron_test.go`

- [ ] **Step 1: Write tests for cronDescribe**

```go
// go/cron_test.go
package main

import (
	"testing"
	"time"
)

func TestCronDescribe(t *testing.T) {
	tests := []struct {
		expr string
		want string
	}{
		{"* * * * *", "Every minute"},
		{"*/5 * * * *", "Every 5 minutes"},
		{"*/15 * * * *", "Every 15 minutes"},
		{"0 * * * *", "Every hour"},
		{"30 * * * *", "Every hour at :30"},
		{"0 8 * * *", "Daily at 8:00 AM"},
		{"0 0 * * *", "Daily at 12:00 AM"},
		{"0 12 * * *", "Daily at 12:00 PM"},
		{"0 13 * * *", "Daily at 1:00 PM"},
		{"30 9 * * *", "Daily at 9:30 AM"},
		{"0 8 * * 1-5", "Weekdays at 8:00 AM"},
		{"0 8 * * 0,6", "Weekends at 8:00 AM"},
		{"0 8 * * 1", "Mondays at 8:00 AM"},
		{"0 8 * * 0", "Sundays at 8:00 AM"},
		{"0 8 * * 5", "Fridays at 8:00 AM"},
		{"0 8 1 * *", "1st of every month at 8:00 AM"},
		// Fallback for complex expressions
		{"0 8 1 6 *", "0 8 1 6 *"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got := cronDescribe(tt.expr)
			if got != tt.want {
				t.Errorf("cronDescribe(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd go && go test -run TestCronDescribe -v`
Expected: FAIL — `cronDescribe` not defined

- [ ] **Step 3: Implement cronDescribe**

```go
// go/cron.go
package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var dayNames = []string{"Sundays", "Mondays", "Tuesdays", "Wednesdays", "Thursdays", "Fridays", "Saturdays"}

func cronDescribe(expr string) string {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return expr
	}
	minute, hour, dom, month, dow := fields[0], fields[1], fields[2], fields[3], fields[4]

	// Every minute
	if minute == "*" && hour == "*" && dom == "*" && month == "*" && dow == "*" {
		return "Every minute"
	}

	// Every N minutes
	if strings.HasPrefix(minute, "*/") && hour == "*" && dom == "*" && month == "*" && dow == "*" {
		return fmt.Sprintf("Every %s minutes", minute[2:])
	}

	// Every hour
	if hour == "*" && dom == "*" && month == "*" && dow == "*" {
		if minute == "0" {
			return "Every hour"
		}
		m, err := strconv.Atoi(minute)
		if err == nil {
			return fmt.Sprintf("Every hour at :%02d", m)
		}
		return expr
	}

	// Specific hour:minute patterns
	h, hErr := strconv.Atoi(hour)
	m, mErr := strconv.Atoi(minute)
	if hErr != nil || mErr != nil {
		return expr
	}
	timeStr := formatTimeAMPM(h, m)

	// Daily / weekday / weekend / specific day
	if dom == "*" && month == "*" {
		if dow == "*" {
			return fmt.Sprintf("Daily at %s", timeStr)
		}
		if dow == "1-5" {
			return fmt.Sprintf("Weekdays at %s", timeStr)
		}
		if dow == "0,6" || dow == "6,0" {
			return fmt.Sprintf("Weekends at %s", timeStr)
		}
		d, err := strconv.Atoi(dow)
		if err == nil && d >= 0 && d <= 6 {
			return fmt.Sprintf("%s at %s", dayNames[d], timeStr)
		}
		return expr
	}

	// Monthly (specific dom, any month)
	if month == "*" && dow == "*" {
		d, err := strconv.Atoi(dom)
		if err == nil {
			return fmt.Sprintf("%s of every month at %s", ordinal(d), timeStr)
		}
	}

	return expr
}

func formatTimeAMPM(h, m int) string {
	ampm := "AM"
	if h >= 12 {
		ampm = "PM"
	}
	display := h % 12
	if display == 0 {
		display = 12
	}
	return fmt.Sprintf("%d:%02d %s", display, m, ampm)
}

func ordinal(n int) string {
	suffix := "th"
	switch n % 10 {
	case 1:
		if n%100 != 11 {
			suffix = "st"
		}
	case 2:
		if n%100 != 12 {
			suffix = "nd"
		}
	case 3:
		if n%100 != 13 {
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd go && go test -run TestCronDescribe -v`
Expected: PASS

- [ ] **Step 5: Write tests for datetimeToCron**

Add to `go/cron_test.go`:

```go
func TestDatetimeToCron(t *testing.T) {
	tests := []struct {
		input string // RFC3339 or "2006-01-02 15:04"
		want  string
	}{
		{"2026-03-15 14:00", "0 14 15 3 *"},
		{"2026-12-25 09:30", "30 9 25 12 *"},
		{"2026-01-01 00:00", "0 0 1 1 *"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ts, err := time.Parse("2006-01-02 15:04", tt.input)
			if err != nil {
				t.Fatal(err)
			}
			got := datetimeToCron(ts)
			if got != tt.want {
				t.Errorf("datetimeToCron(%v) = %q, want %q", ts, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `cd go && go test -run TestDatetimeToCron -v`
Expected: FAIL — `datetimeToCron` not defined

- [ ] **Step 7: Implement datetimeToCron**

Add to `go/cron.go` (note: `"time"` is already in the import block):

```go
func datetimeToCron(t time.Time) string {
	return fmt.Sprintf("%d %d %d %d *", t.Minute(), t.Hour(), t.Day(), int(t.Month()))
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `cd go && go test -run TestDatetimeToCron -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
cd go && git add cron.go cron_test.go && git commit -m "feat: add cron expression utilities"
```

---

### Task 2: Schedule Types and Crontab Manipulation

**Files:**
- Create: `go/schedule.go`
- Create: `go/schedule_test.go`

- [ ] **Step 1: Write tests for name validation**

```go
// go/schedule_test.go
package main

import "testing"

func TestValidateName(t *testing.T) {
	valid := []string{"daily-review", "my_task", "task1", "A-b-C"}
	for _, name := range valid {
		if err := validateScheduleName(name); err != nil {
			t.Errorf("validateScheduleName(%q) returned error: %v", name, err)
		}
	}
	invalid := []string{"", "has spaces", "has/slash", "has.dot", "a@b", "hello!"}
	for _, name := range invalid {
		if err := validateScheduleName(name); err == nil {
			t.Errorf("validateScheduleName(%q) should have returned error", name)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go && go test -run TestValidateName -v`
Expected: FAIL

- [ ] **Step 3: Implement Schedule type, validateScheduleName, and path helpers**

```go
// go/schedule.go
package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type Schedule struct {
	Name         string
	Cron         string
	CWD          string
	Launcher     string
	ExitOnFinish bool
	Once         bool
	Disabled     bool
	BinaryPath   string
}

type RunPointer struct {
	UUID   string `json:"uuid"`
	CWD    string `json:"cwd"`
	Status string `json:"status"`
}

type RunInfo struct {
	Filename  string
	Timestamp string
	Pointer   RunPointer
}

func validateScheduleName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if !nameRegex.MatchString(name) {
		return fmt.Errorf("name must match [a-zA-Z0-9_-]+, got %q", name)
	}
	return nil
}

func promptsDir() string {
	return filepath.Join(stateDir(), "prompts")
}

func runsDir() string {
	return filepath.Join(stateDir(), "runs")
}

func promptPath(name string) string {
	return filepath.Join(promptsDir(), name+".txt")
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd go && go test -run TestValidateName -v`
Expected: PASS

- [ ] **Step 5: Write tests for crontab parsing**

Add to `go/schedule_test.go`:

```go
func TestParseCrontab(t *testing.T) {
	content := `# some other cron job
0 * * * * /usr/bin/something
# lila:daily-review
0 8 * * * bash -l -c '/usr/local/bin/lila run-scheduled --name daily-review --cwd /home/user/repo/myproject --launcher claude --exit-on-finish'
# lila:weekly-cleanup
#DISABLED# 0 9 * * 1 bash -l -c '/usr/local/bin/lila run-scheduled --name weekly-cleanup --cwd /home/user/repo/api --launcher codex'
# lila:one-shot
0 14 15 3 * bash -l -c '/usr/local/bin/lila run-scheduled --name one-shot --cwd /home/user/repo/foo --launcher claude --exit-on-finish --once'
`
	schedules := parseCrontab(content)
	if len(schedules) != 3 {
		t.Fatalf("expected 3 schedules, got %d", len(schedules))
	}

	s := schedules[0]
	if s.Name != "daily-review" {
		t.Errorf("name = %q, want daily-review", s.Name)
	}
	if s.Cron != "0 8 * * *" {
		t.Errorf("cron = %q, want '0 8 * * *'", s.Cron)
	}
	if s.CWD != "/home/user/repo/myproject" {
		t.Errorf("cwd = %q", s.CWD)
	}
	if s.Launcher != "claude" {
		t.Errorf("launcher = %q", s.Launcher)
	}
	if !s.ExitOnFinish {
		t.Error("expected exit-on-finish")
	}
	if s.Disabled {
		t.Error("should not be disabled")
	}
	if s.Once {
		t.Error("should not be once")
	}

	s2 := schedules[1]
	if s2.Name != "weekly-cleanup" {
		t.Errorf("name = %q", s2.Name)
	}
	if !s2.Disabled {
		t.Error("should be disabled")
	}
	if s2.Launcher != "codex" {
		t.Errorf("launcher = %q", s2.Launcher)
	}

	s3 := schedules[2]
	if !s3.Once {
		t.Error("should be once")
	}
	if !s3.ExitOnFinish {
		t.Error("should be exit-on-finish")
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `cd go && go test -run TestParseCrontab -v`
Expected: FAIL

- [ ] **Step 7: Implement parseCrontab**

Add to `go/schedule.go`:

```go
func parseCrontab(content string) []Schedule {
	lines := strings.Split(content, "\n")
	var schedules []Schedule
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "# lila:") {
			continue
		}
		name := strings.TrimPrefix(line, "# lila:")
		if i+1 >= len(lines) {
			continue
		}
		i++
		s := parseScheduleLine(name, lines[i])
		if s != nil {
			schedules = append(schedules, *s)
		}
	}
	return schedules
}

func parseScheduleLine(name, line string) *Schedule {
	s := &Schedule{Name: name}

	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "#DISABLED# ") {
		s.Disabled = true
		line = strings.TrimPrefix(line, "#DISABLED# ")
	}

	// Parse 5 cron fields
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return nil
	}
	s.Cron = strings.Join(fields[:5], " ")

	// Extract inner command from bash -l -c '...'
	rest := strings.Join(fields[5:], " ")
	start := strings.Index(rest, "'")
	end := strings.LastIndex(rest, "'")
	if start < 0 || end <= start {
		return nil
	}
	innerCmd := rest[start+1 : end]

	// Find run-scheduled and parse flags after it
	idx := strings.Index(innerCmd, "run-scheduled")
	if idx < 0 {
		return nil
	}

	// Extract binary path (everything before "run-scheduled")
	s.BinaryPath = strings.TrimSpace(innerCmd[:idx])

	flagStr := innerCmd[idx+len("run-scheduled"):]
	flagFields := strings.Fields(flagStr)
	for j := 0; j < len(flagFields); j++ {
		switch flagFields[j] {
		case "--name":
			if j+1 < len(flagFields) {
				j++ // skip value, name already from comment
			}
		case "--cwd":
			if j+1 < len(flagFields) {
				j++
				s.CWD = flagFields[j]
			}
		case "--launcher":
			if j+1 < len(flagFields) {
				j++
				s.Launcher = flagFields[j]
			}
		case "--exit-on-finish":
			s.ExitOnFinish = true
		case "--once":
			s.Once = true
		}
	}

	return s
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `cd go && go test -run TestParseCrontab -v`
Expected: PASS

- [ ] **Step 9: Write tests for buildCrontabBlock**

Add to `go/schedule_test.go`:

```go
func TestBuildCrontabBlock(t *testing.T) {
	s := Schedule{
		Name:         "daily-review",
		Cron:         "0 8 * * *",
		CWD:          "/home/user/repo/myproject",
		Launcher:     "claude",
		ExitOnFinish: true,
		BinaryPath:   "/usr/local/bin/lila",
	}
	got := buildCrontabBlock(s)
	want := "# lila:daily-review\n0 8 * * * bash -l -c '/usr/local/bin/lila run-scheduled --name daily-review --cwd /home/user/repo/myproject --launcher claude --exit-on-finish'"
	if got != want {
		t.Errorf("buildCrontabBlock:\ngot:  %q\nwant: %q", got, want)
	}

	// Disabled
	s.Disabled = true
	got = buildCrontabBlock(s)
	if !strings.HasPrefix(strings.Split(got, "\n")[1], "#DISABLED# ") {
		t.Errorf("disabled entry should start with #DISABLED#, got: %q", got)
	}

	// Once
	s.Disabled = false
	s.Once = true
	got = buildCrontabBlock(s)
	if !strings.Contains(got, "--once") {
		t.Error("once entry should contain --once flag")
	}
}

func TestRoundTrip(t *testing.T) {
	original := Schedule{
		Name:         "my-task",
		Cron:         "30 9 * * 1-5",
		CWD:          "/home/user/repo/api",
		Launcher:     "codex",
		ExitOnFinish: false,
		Once:         true,
		Disabled:     false,
		BinaryPath:   "/usr/local/bin/lila",
	}
	block := buildCrontabBlock(original)
	parsed := parseCrontab(block)
	if len(parsed) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(parsed))
	}
	s := parsed[0]
	if s.Name != original.Name || s.Cron != original.Cron || s.CWD != original.CWD ||
		s.Launcher != original.Launcher || s.ExitOnFinish != original.ExitOnFinish ||
		s.Once != original.Once || s.Disabled != original.Disabled {
		t.Errorf("round-trip mismatch:\noriginal: %+v\nparsed:   %+v", original, s)
	}
}
```

- [ ] **Step 10: Run tests to verify they fail**

Run: `cd go && go test -run "TestBuildCrontabBlock|TestRoundTrip" -v`
Expected: FAIL

- [ ] **Step 11: Implement buildCrontabBlock**

Add to `go/schedule.go`:

```go
func buildCrontabBlock(s Schedule) string {
	var parts []string
	parts = append(parts, s.BinaryPath, "run-scheduled")
	parts = append(parts, "--name", s.Name)
	parts = append(parts, "--cwd", s.CWD)
	parts = append(parts, "--launcher", s.Launcher)
	if s.ExitOnFinish {
		parts = append(parts, "--exit-on-finish")
	}
	if s.Once {
		parts = append(parts, "--once")
	}
	innerCmd := strings.Join(parts, " ")

	cmdLine := fmt.Sprintf("%s bash -l -c '%s'", s.Cron, innerCmd)
	if s.Disabled {
		cmdLine = "#DISABLED# " + cmdLine
	}
	return fmt.Sprintf("# lila:%s\n%s", s.Name, cmdLine)
}
```

- [ ] **Step 12: Run tests to verify they pass**

Run: `cd go && go test -run "TestBuildCrontabBlock|TestRoundTrip" -v`
Expected: PASS

- [ ] **Step 13: Implement run pointer file I/O and run listing**

Add to `go/schedule.go`:

```go
func writeRunPointer(name, uuid, cwd string) (string, error) {
	dir := runsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	ts := time.Now().Format("20060102T150405")
	filename := fmt.Sprintf("%s-%s.json", name, ts)
	path := filepath.Join(dir, filename)
	ptr := RunPointer{UUID: uuid, CWD: cwd, Status: "running"}
	data, err := json.Marshal(ptr)
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0644)
}

func updateRunPointerStatus(path, status string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var ptr RunPointer
	if err := json.Unmarshal(data, &ptr); err != nil {
		return err
	}
	ptr.Status = status
	out, err := json.Marshal(ptr)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0644)
}

func listRuns(name string) []RunInfo {
	dir := runsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	prefix := name + "-"
	var runs []RunInfo
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var ptr RunPointer
		if err := json.Unmarshal(data, &ptr); err != nil {
			continue
		}
		tsStr := strings.TrimSuffix(strings.TrimPrefix(e.Name(), prefix), ".json")
		runs = append(runs, RunInfo{
			Filename:  e.Name(),
			Timestamp: tsStr,
			Pointer:   ptr,
		})
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].Timestamp > runs[j].Timestamp
	})
	return runs
}

func listAllRuns() []RunInfo {
	dir := runsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var runs []RunInfo
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var ptr RunPointer
		if err := json.Unmarshal(data, &ptr); err != nil {
			continue
		}
		// Extract name: everything before the last -TIMESTAMP.json
		base := strings.TrimSuffix(e.Name(), ".json")
		// Timestamp format is 20060102T150405 (15 chars)
		if len(base) < 16 {
			continue
		}
		runs = append(runs, RunInfo{
			Filename:  e.Name(),
			Timestamp: base[len(base)-15:],
			Pointer:   ptr,
		})
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].Timestamp > runs[j].Timestamp
	})
	return runs
}

// runNameFromFilename extracts the schedule name from a run pointer filename.
// Format: <name>-<timestamp>.json where timestamp is 20060102T150405.
func runNameFromFilename(filename string) string {
	base := strings.TrimSuffix(filename, ".json")
	// Timestamp is 15 chars (20060102T150405) + 1 dash separator
	if len(base) < 16 {
		return base
	}
	return base[:len(base)-16]
}
```

- [ ] **Step 14: Implement crontab system operations**

Add to `go/schedule.go`:

```go
func readCrontab() (string, error) {
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		// No crontab yet is not an error
		return "", nil
	}
	return string(out), nil
}

func writeCrontab(content string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	return cmd.Run()
}

func addScheduleToCrontab(s Schedule) error {
	content, err := readCrontab()
	if err != nil {
		return err
	}
	// Check for existing schedule with same name
	existing := parseCrontab(content)
	for _, e := range existing {
		if e.Name == s.Name {
			return fmt.Errorf("schedule %q already exists", s.Name)
		}
	}
	block := buildCrontabBlock(s)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += block + "\n"
	return writeCrontab(content)
}

func removeScheduleFromCrontab(name string) error {
	content, err := readCrontab()
	if err != nil {
		return err
	}
	newContent := removeCrontabBlock(content, name)
	return writeCrontab(newContent)
}

func removeCrontabBlock(content, name string) string {
	marker := "# lila:" + name
	lines := strings.Split(content, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == marker {
			// Skip marker and the following command line
			if i+1 < len(lines) {
				i++
			}
			continue
		}
		out = append(out, lines[i])
	}
	return strings.Join(out, "\n")
}

func enableScheduleInCrontab(name string) error {
	content, err := readCrontab()
	if err != nil {
		return err
	}
	marker := "# lila:" + name
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == marker && i+1 < len(lines) {
			next := lines[i+1]
			if strings.HasPrefix(strings.TrimSpace(next), "#DISABLED# ") {
				lines[i+1] = strings.Replace(next, "#DISABLED# ", "", 1)
			}
		}
	}
	return writeCrontab(strings.Join(lines, "\n"))
}

func disableScheduleInCrontab(name string) error {
	content, err := readCrontab()
	if err != nil {
		return err
	}
	marker := "# lila:" + name
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == marker && i+1 < len(lines) {
			next := lines[i+1]
			if !strings.HasPrefix(strings.TrimSpace(next), "#DISABLED# ") {
				lines[i+1] = "#DISABLED# " + strings.TrimSpace(next)
			}
		}
	}
	return writeCrontab(strings.Join(lines, "\n"))
}

func replaceScheduleInCrontab(s Schedule) error {
	content, err := readCrontab()
	if err != nil {
		return err
	}
	content = removeCrontabBlock(content, s.Name)
	block := buildCrontabBlock(s)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += block + "\n"
	return writeCrontab(content)
}

func getScheduleFromCrontab(name string) *Schedule {
	content, err := readCrontab()
	if err != nil {
		return nil
	}
	for _, s := range parseCrontab(content) {
		if s.Name == name {
			return &s
		}
	}
	return nil
}

func listSchedules() []Schedule {
	content, err := readCrontab()
	if err != nil {
		return nil
	}
	return parseCrontab(content)
}
```

- [ ] **Step 15: Implement session log resolution and token counting**

Add to `go/schedule.go`:

```go
func findSessionLog(cwd, uuid string) string {
	home, _ := os.UserHomeDir()
	projectsDir := filepath.Join(home, ".claude", "projects")
	var found string
	filepath.Walk(projectsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found != "" {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if base == uuid {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func sessionTokens(logPath string) int64 {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return 0
	}
	var total int64
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, `"type":"assistant"`) {
			continue
		}
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["type"] != "assistant" {
			continue
		}
		msg, ok := entry["message"].(map[string]interface{})
		if !ok {
			continue
		}
		usage, ok := msg["usage"].(map[string]interface{})
		if !ok {
			continue
		}
		total += jsonInt(usage, "input_tokens") + jsonInt(usage, "output_tokens") +
			jsonInt(usage, "cache_creation_input_tokens") + jsonInt(usage, "cache_read_input_tokens")
	}
	return total
}
```

- [ ] **Step 16: Run all tests**

Run: `cd go && go test -v`
Expected: All pass, no compilation errors

- [ ] **Step 17: Commit**

```bash
cd go && git add schedule.go schedule_test.go && git commit -m "feat: add schedule types, crontab manipulation, and file I/O"
```

---

## Chunk 2: Commands and Daemon

### Task 3: run-scheduled Command and CLI Routing

**Files:**
- Create: `go/schedule_cli.go`
- Modify: `go/main.go`

- [ ] **Step 1: Implement the run-scheduled command**

```go
// go/schedule_cli.go
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runScheduledCmd(args []string) {
	var name, cwd, launcher string
	var exitOnFinish, once bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 < len(args) {
				i++
				name = args[i]
			}
		case "--cwd":
			if i+1 < len(args) {
				i++
				cwd = args[i]
			}
		case "--launcher":
			if i+1 < len(args) {
				i++
				launcher = args[i]
			}
		case "--exit-on-finish":
			exitOnFinish = true
		case "--once":
			once = true
		}
	}

	if name == "" || cwd == "" || launcher == "" {
		fmt.Fprintln(os.Stderr, "run-scheduled: --name, --cwd, and --launcher are required")
		os.Exit(1)
	}

	// Step 1: Generate UUID
	uuid := newUUID()

	// Step 2: Read prompt
	prompt, err := os.ReadFile(promptPath(name))
	if err != nil {
		fmt.Fprintf(os.Stderr, "run-scheduled: cannot read prompt file: %v\n", err)
		os.Exit(1)
	}
	promptText := strings.TrimSpace(string(prompt))
	if promptText == "" {
		fmt.Fprintln(os.Stderr, "run-scheduled: prompt file is empty")
		os.Exit(1)
	}

	// Step 3: One-shot self-cleanup
	if once {
		removeScheduleFromCrontab(name)
	}

	// Step 4: Write run pointer
	_, err = writeRunPointer(name, uuid, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run-scheduled: cannot write run pointer: %v\n", err)
		os.Exit(1)
	}

	// Step 5-6: Launch tmux session
	sessionName := fmt.Sprintf("sched-%s-%s", name, uuid[:8])

	if exitOnFinish {
		// Pipe prompt file into claude via stdin with -p flag (print mode, exits when done)
		cmd := fmt.Sprintf("cat %s | %s --dangerously-skip-permissions --session-id %s -p",
			shellQuote(promptPath(name)), launcher, uuid)
		if err := tmuxNewSession(sessionName, cwd, cmd); err != nil {
			fmt.Fprintf(os.Stderr, "run-scheduled: cannot create tmux session: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Launch interactive session, then send prompt via send-keys
		cmd := fmt.Sprintf("%s --dangerously-skip-permissions --session-id %s", launcher, uuid)
		if err := tmuxNewSession(sessionName, cwd, cmd); err != nil {
			fmt.Fprintf(os.Stderr, "run-scheduled: cannot create tmux session: %v\n", err)
			os.Exit(1)
		}
		// Wait briefly for the session to start, then send prompt
		time.Sleep(2 * time.Second)
		exec.Command("tmux", "send-keys", "-t", sessionName, promptText, "Enter").Run()
	}

	fmt.Printf("run-scheduled: launched session %s (uuid: %s)\n", sessionName, uuid)
}
```

- [ ] **Step 2: Implement schedule CLI subcommands**

Add to `go/schedule_cli.go`:

```go
func scheduleCmd(args []string) {
	if len(args) == 0 {
		scheduleHelp()
		return
	}
	switch args[0] {
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: lila schedule add <name>")
			os.Exit(1)
		}
		scheduleAdd(args[1])
	case "list":
		scheduleList()
	case "rm":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: lila schedule rm <name>")
			os.Exit(1)
		}
		scheduleRm(args[1])
	case "edit":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: lila schedule edit <name>")
			os.Exit(1)
		}
		scheduleEdit(args[1])
	case "runs":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: lila schedule runs <name>")
			os.Exit(1)
		}
		scheduleRuns(args[1])
	case "enable":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: lila schedule enable <name>")
			os.Exit(1)
		}
		if err := enableScheduleInCrontab(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Enabled schedule %q\n", args[1])
	case "disable":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: lila schedule disable <name>")
			os.Exit(1)
		}
		if err := disableScheduleInCrontab(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Disabled schedule %q\n", args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown schedule command: %s\n", args[0])
		scheduleHelp()
		os.Exit(1)
	}
}

func scheduleHelp() {
	fmt.Println("Usage: lila schedule <command> [name]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  add <name>       Create a new schedule")
	fmt.Println("  list             List all schedules")
	fmt.Println("  rm <name>        Remove a schedule")
	fmt.Println("  edit <name>      Edit an existing schedule")
	fmt.Println("  runs <name>      Show past runs for a schedule")
	fmt.Println("  enable <name>    Enable a disabled schedule")
	fmt.Println("  disable <name>   Disable a schedule without deleting")
}

func scheduleAdd(name string) {
	if err := validateScheduleName(name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	// Check if already exists
	if s := getScheduleFromCrontab(name); s != nil {
		fmt.Fprintf(os.Stderr, "Error: schedule %q already exists\n", name)
		os.Exit(1)
	}

	reader := bufio.NewReader(os.Stdin)

	// Recurring or one-time
	fmt.Print("  Recurring or one-time? [r/o]: ")
	mode, _ := reader.ReadString('\n')
	mode = strings.TrimSpace(mode)

	var cronExpr string
	var once bool

	if mode == "o" || mode == "O" {
		fmt.Print("  When? (YYYY-MM-DD HH:MM): ")
		when, _ := reader.ReadString('\n')
		when = strings.TrimSpace(when)
		t, err := time.Parse("2006-01-02 15:04", when)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid datetime: %v\n", err)
			os.Exit(1)
		}
		cronExpr = datetimeToCron(t)
		once = true
		fmt.Printf("  → %s (once)\n", cronDescribe(cronExpr))
	} else {
		fmt.Print("  Cron expression (e.g. '0 8 * * *' for daily at 8AM): ")
		cronExpr, _ = reader.ReadString('\n')
		cronExpr = strings.TrimSpace(cronExpr)
		desc := cronDescribe(cronExpr)
		fmt.Printf("  → %s. Correct? [y/n]: ", desc)
		confirm, _ := reader.ReadString('\n')
		if strings.TrimSpace(confirm) != "y" && strings.TrimSpace(confirm) != "Y" {
			fmt.Println("  Cancelled.")
			return
		}
	}

	// Select repo
	fmt.Println("  Select repo:")
	repos := listRepos()
	home, _ := os.UserHomeDir()
	for i, r := range repos {
		fmt.Printf("    %s) %s\n", keyLabel(i, allKeys), r)
	}
	fmt.Print("  Selection: ")
	rsel, _ := reader.ReadString('\n')
	rsel = strings.TrimSpace(rsel)
	ridx, valid := keyIdx(rsel, allKeys)
	if !valid || ridx >= len(repos) {
		fmt.Fprintln(os.Stderr, "Error: invalid selection")
		os.Exit(1)
	}
	cwd := filepath.Join(home, "repo", repos[ridx])

	// Launcher
	fmt.Print("  Launcher [claude/codex] (default: claude): ")
	launcher, _ := reader.ReadString('\n')
	launcher = strings.TrimSpace(launcher)
	if launcher == "" {
		launcher = "claude"
	}
	if launcher != "claude" && launcher != "codex" {
		fmt.Fprintln(os.Stderr, "Error: launcher must be 'claude' or 'codex'")
		os.Exit(1)
	}

	// Exit on finish
	fmt.Print("  Exit when done? [y/n] (default: y): ")
	exitStr, _ := reader.ReadString('\n')
	exitStr = strings.TrimSpace(exitStr)
	exitOnFinish := exitStr == "" || exitStr == "y" || exitStr == "Y"

	// Prompt — open $EDITOR
	pDir := promptsDir()
	os.MkdirAll(pDir, 0755)
	pPath := promptPath(name)
	// Create empty file if it doesn't exist
	if _, err := os.Stat(pPath); os.IsNotExist(err) {
		os.WriteFile(pPath, []byte(""), 0644)
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nano"
	}
	fmt.Printf("  Opening %s to edit prompt...\n", editor)
	cmd := exec.Command(editor, pPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: editor failed: %v\n", err)
		os.Exit(1)
	}
	// Verify prompt is not empty
	promptData, _ := os.ReadFile(pPath)
	if strings.TrimSpace(string(promptData)) == "" {
		fmt.Fprintln(os.Stderr, "Error: prompt is empty, aborting")
		os.Remove(pPath)
		os.Exit(1)
	}

	// Resolve binary path
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot find executable path: %v\n", err)
		os.Exit(1)
	}
	exe, _ = filepath.EvalSymlinks(exe)

	s := Schedule{
		Name:         name,
		Cron:         cronExpr,
		CWD:          cwd,
		Launcher:     launcher,
		ExitOnFinish: exitOnFinish,
		Once:         once,
		BinaryPath:   exe,
	}

	if err := addScheduleToCrontab(s); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Schedule %q created: %s (%s)\n", name, cronExpr, cronDescribe(cronExpr))
}

func scheduleList() {
	schedules := listSchedules()
	if len(schedules) == 0 {
		fmt.Println("  No schedules configured.")
		return
	}
	home, _ := os.UserHomeDir()
	repoPrefix := filepath.Join(home, "repo") + "/"
	for _, s := range schedules {
		desc := cronDescribe(s.Cron)
		status := "enabled"
		if s.Disabled {
			status = "disabled"
		}
		if s.Once {
			desc = "(once)"
		}
		dir := s.CWD
		if strings.HasPrefix(dir, repoPrefix) {
			dir = dir[len(repoPrefix):]
		}
		fmt.Printf("  %-20s %s (%s)  %s  %s\n", s.Name, s.Cron, desc, dir, status)
	}
}

func scheduleRm(name string) {
	if err := removeScheduleFromCrontab(name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	// Remove prompt file
	os.Remove(promptPath(name))
	// Remove run pointer files
	runs := listRuns(name)
	for _, r := range runs {
		os.Remove(filepath.Join(runsDir(), r.Filename))
	}
	fmt.Printf("  Removed schedule %q\n", name)
}

func scheduleEdit(name string) {
	existing := getScheduleFromCrontab(name)
	if existing == nil {
		fmt.Fprintf(os.Stderr, "Error: schedule %q not found\n", name)
		os.Exit(1)
	}

	reader := bufio.NewReader(os.Stdin)

	// Cron expression
	fmt.Printf("  Cron expression [%s] (%s): ", existing.Cron, cronDescribe(existing.Cron))
	cronExpr, _ := reader.ReadString('\n')
	cronExpr = strings.TrimSpace(cronExpr)
	if cronExpr == "" {
		cronExpr = existing.Cron
	} else {
		fmt.Printf("  → %s\n", cronDescribe(cronExpr))
	}

	// CWD
	fmt.Printf("  Working directory [%s]: ", existing.CWD)
	cwd, _ := reader.ReadString('\n')
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = existing.CWD
	}

	// Launcher
	fmt.Printf("  Launcher [%s]: ", existing.Launcher)
	launcher, _ := reader.ReadString('\n')
	launcher = strings.TrimSpace(launcher)
	if launcher == "" {
		launcher = existing.Launcher
	}

	// Exit on finish
	exitDefault := "n"
	if existing.ExitOnFinish {
		exitDefault = "y"
	}
	fmt.Printf("  Exit when done? [%s]: ", exitDefault)
	exitStr, _ := reader.ReadString('\n')
	exitStr = strings.TrimSpace(exitStr)
	exitOnFinish := existing.ExitOnFinish
	if exitStr == "y" || exitStr == "Y" {
		exitOnFinish = true
	} else if exitStr == "n" || exitStr == "N" {
		exitOnFinish = false
	}

	// Prompt
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nano"
	}
	fmt.Printf("  Edit prompt in %s? [y/n] (default: n): ", editor)
	editPrompt, _ := reader.ReadString('\n')
	if strings.TrimSpace(editPrompt) == "y" || strings.TrimSpace(editPrompt) == "Y" {
		cmd := exec.Command(editor, promptPath(name))
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
	}

	// Resolve binary path
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)

	s := Schedule{
		Name:         name,
		Cron:         cronExpr,
		CWD:          cwd,
		Launcher:     launcher,
		ExitOnFinish: exitOnFinish,
		Once:         existing.Once,
		Disabled:     existing.Disabled,
		BinaryPath:   exe,
	}

	if err := replaceScheduleInCrontab(s); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Updated schedule %q\n", name)
}

func scheduleRuns(name string) {
	runs := listRuns(name)
	if len(runs) == 0 {
		fmt.Printf("  No runs for %q\n", name)
		return
	}
	for _, r := range runs {
		ts := formatRunTimestamp(r.Timestamp)
		tokens := "-"
		logPath := findSessionLog(r.Pointer.CWD, r.Pointer.UUID)
		if logPath != "" {
			tokens = fmtTokens(sessionTokens(logPath))
		}
		fmt.Printf("  %s  %-8s  %s tokens\n", ts, r.Pointer.Status, tokens)
	}
}

// formatRunTimestamp converts "20260314T080000" to "2026-03-14 08:00"
func formatRunTimestamp(ts string) string {
	t, err := time.Parse("20060102T150405", ts)
	if err != nil {
		return ts
	}
	return t.Format("2006-01-02 15:04")
}
```

- [ ] **Step 3: Update main.go with command routing**

Modify `go/main.go` to add `schedule` and `run-scheduled` cases:

```go
// In main(), add to the switch statement after "uninstall-service":
case "schedule":
	scheduleCmd(os.Args[2:])
	return
case "run-scheduled":
	runScheduledCmd(os.Args[2:])
	return
```

Also update `printHelp()`:

```go
func printHelp() {
	fmt.Println("Usage: lila [command] [flags]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  daemon              Run background unread detection service")
	fmt.Println("  install-service     Install and start as system service")
	fmt.Println("  uninstall-service   Stop and remove system service")
	fmt.Println("  schedule            Manage scheduled agents")
	fmt.Println("  run-scheduled       Internal: execute a scheduled agent (called by cron)")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --prompt-me         Ask for a starting prompt when creating sessions")
	fmt.Println("  --quota             Print quota info and exit")
}
```

- [ ] **Step 4: Verify compilation**

Run: `cd go && go build -o lila .`
Expected: Compiles without errors

- [ ] **Step 5: Commit**

```bash
cd go && git add schedule_cli.go main.go && git commit -m "feat: add schedule CLI commands and run-scheduled execution"
```

---

### Task 4: Daemon Changes

**Files:**
- Modify: `go/daemon.go`

- [ ] **Step 1: Add sched-* session tracking to daemon**

In `daemon.go`, modify the `checkUnread` function to also track scheduled session transitions. Add a new function and modify the existing loop:

```go
// Add to checkUnread, after the "Clean up prevStates for dead sessions" block:

// Check for completed scheduled runs
checkSchedRuns(alive)
```

Add the new function:

```go
// checkSchedRuns checks for sched-* sessions that have ended
// and updates their run pointer files.
func checkSchedRuns(aliveSessions map[string]bool) {
	dir := runsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var ptr RunPointer
		if err := json.Unmarshal(data, &ptr); err != nil {
			continue
		}
		if ptr.Status != "running" {
			continue
		}
		// Check if the tmux session is still alive by matching the specific UUID
		name := runNameFromFilename(e.Name())
		uuidPrefix := ptr.UUID
		if len(uuidPrefix) > 8 {
			uuidPrefix = uuidPrefix[:8]
		}
		sessionName := fmt.Sprintf("sched-%s-%s", name, uuidPrefix)
		if aliveSessions[sessionName] {
			continue
		}
		// Session is gone — update status
		logPath := findSessionLog(ptr.CWD, ptr.UUID)
		status := "error"
		if logPath != "" {
			// Check if the log has content
			info, err := os.Stat(logPath)
			if err == nil && info.Size() > 0 {
				status = "exited"
			}
		}
		updateRunPointerStatus(filepath.Join(dir, e.Name()), status)
	}
}
```

Add `"encoding/json"` to daemon.go's import block. Note: `"path/filepath"`, `"os"`, `"strings"`, and `"fmt"` are already imported.

Note: `runsDir()`, `RunPointer`, `runNameFromFilename()`, `findSessionLog()`, and `updateRunPointerStatus()` are all defined in `schedule.go`.

- [ ] **Step 2: Verify compilation**

Run: `cd go && go build -o lila .`
Expected: Compiles without errors

- [ ] **Step 3: Commit**

```bash
cd go && git add daemon.go && git commit -m "feat: add scheduled run status tracking to daemon"
```

---

## Chunk 3: TUI

### Task 5: TUI Schedules Screen

**Files:**
- Modify: `go/tui.go`

This task modifies the TUI to add a schedules screen with selection mode and detail view. The changes are all within `tui.go`.

- [ ] **Step 1: Add [r] to reserved keys and render it in the footer**

In `runTUI()`, add `'r'` to the reserved keys map:

```go
reserved := map[byte]bool{
	'k': true, // kill
	'n': true, // new
	'c': true, // toggle cli
	'r': true, // schedules
}
```

Update the footer rendering to include `[r]`:

In the footer where `[n] new  [c] cli:...` is rendered, add `[r] recurring` before `[esc] quit`. Both the sessions-present and no-sessions cases need updating.

- [ ] **Step 2: Add screen state variables**

Add at the top of `runTUI()`, after the existing variable declarations:

```go
screen := "main" // "main", "schedules", "detail"
var schedSelection int = -1 // -1 = no selection
var detailSchedule string   // name of schedule in detail view
```

- [ ] **Step 3: Handle [r] keypress to switch to schedules screen**

In the input handling section, add a case for `r`:

```go
} else if sel == "r" || sel == "R" {
	screen = "schedules"
	schedSelection = -1
	continue
```

- [ ] **Step 4: Wrap existing main screen rendering in a screen switch**

Wrap the existing rendering and input handling code inside `if screen == "main" { ... }`. Then add `else if screen == "schedules" { ... }` and `else if screen == "detail" { ... }` blocks.

- [ ] **Step 5: Implement schedules screen rendering**

In the `screen == "schedules"` block, render scheduled agents and past runs:

```go
} else if screen == "schedules" {
	schedules := listSchedules()
	allRunsList := listAllRuns()

	// Limit past runs to most recent 20
	if len(allRunsList) > 20 {
		allRunsList = allRunsList[:20]
	}

	totalItems := len(schedules) + len(allRunsList)
	schedKeys := activeKeys(map[byte]bool{'n': true})

	var buf strings.Builder
	buf.WriteString("  \033[1;37mScheduled\033[0m\033[K\r\n")
	if len(schedules) == 0 {
		buf.WriteString("    No schedules configured\033[K\r\n")
	}
	for i, s := range schedules {
		desc := cronDescribe(s.Cron)
		if s.Once {
			desc = "once"
		}
		indicator := "\033[1;32m◆\033[0m"
		if s.Disabled {
			indicator = "\033[0;90m◇\033[0m"
		}
		sel := ""
		if schedSelection == i {
			sel = "\033[1;37m> \033[0m"
		} else {
			sel = "  "
		}
		dir := s.CWD
		if strings.HasPrefix(dir, repoPrefix) {
			dir = dir[len(repoPrefix):]
		}
		buf.WriteString(fmt.Sprintf("  %s\033[1;37m%s)\033[0m %s %s  \033[0;90m%s (%s)  [%s]\033[0m\033[K\r\n",
			sel, keyLabel(i, schedKeys), indicator, s.Name, s.Cron, desc, dir))
	}

	buf.WriteString("\033[K\r\n")
	buf.WriteString("  \033[1;37mPast Runs\033[0m\033[K\r\n")
	if len(allRunsList) == 0 {
		buf.WriteString("    No past runs\033[K\r\n")
	}
	for i, r := range allRunsList {
		idx := len(schedules) + i
		rName := runNameFromFilename(r.Filename)
		ts := formatRunTimestamp(r.Timestamp)
		statusIcon := "\033[1;32m✓\033[0m"
		if r.Pointer.Status == "error" {
			statusIcon = "\033[1;31m✗\033[0m"
		} else if r.Pointer.Status == "running" {
			statusIcon = "\033[1;33m⟳\033[0m"
		}
		tokens := "-"
		logPath := findSessionLog(r.Pointer.CWD, r.Pointer.UUID)
		if logPath != "" {
			t := sessionTokens(logPath)
			if t > 0 {
				tokens = fmtTokens(t)
			}
		}
		sel := ""
		if schedSelection == idx {
			sel = "\033[1;37m> \033[0m"
		} else {
			sel = "  "
		}
		buf.WriteString(fmt.Sprintf("  %s\033[1;37m%s)\033[0m %s %s  %s  %-8s  %s tokens\033[K\r\n",
			sel, keyLabel(idx, schedKeys), statusIcon, rName, ts, r.Pointer.Status, tokens))
	}

	buf.WriteString("\033[K\r\n")
	// Command palette
	if schedSelection >= 0 && schedSelection < len(schedules) {
		buf.WriteString("  \033[0;90m[space] enable/disable  [d]elete  [enter] view details  [esc] deselect\033[0m\033[K")
	} else if schedSelection >= len(schedules) && schedSelection < totalItems {
		buf.WriteString("  \033[0;90m[enter] view log  [d]elete log  [esc] deselect\033[0m\033[K")
	} else {
		buf.WriteString("  \033[0;90m[n]ew schedule  [esc] back\033[0m\033[K")
	}

	if first {
		fmt.Print("\033[2J\033[H")
		first = false
	} else {
		fmt.Print("\033[H")
	}
	fmt.Print(buf.String())
	fmt.Print("\033[J")

	sel, ok := readSel(500 * time.Millisecond)
	if !ok {
		continue
	}

	if sel == "\x1b" {
		if schedSelection >= 0 {
			schedSelection = -1
		} else {
			screen = "main"
			first = true
		}
		continue
	}

	if schedSelection < 0 {
		// No selection — interpret as item selection or command
		if sel == "n" || sel == "N" {
			// New schedule — restore terminal, run interactive add
			term.Restore(fd, oldState)
			fmt.Print("\033[?25h\r\n\r\n")
			fmt.Print("  Schedule name: ")
			name := readLine()
			if name != "" {
				scheduleAdd(name)
			}
			fmt.Print("\r\n  Press any key to continue...")
			term.MakeRaw(fd)
			readByte()
			term.Restore(fd, oldState)
			oldState, _ = term.MakeRaw(fd)
			fmt.Print("\033[?25l")
			first = true
			continue
		}
		idx, valid := keyIdx(sel, schedKeys)
		if valid && idx < totalItems {
			schedSelection = idx
		}
	} else if schedSelection < len(schedules) {
		// Schedule selected
		s := schedules[schedSelection]
		if sel == " " {
			// Toggle enable/disable
			if s.Disabled {
				enableScheduleInCrontab(s.Name)
			} else {
				disableScheduleInCrontab(s.Name)
			}
			schedSelection = -1
		} else if sel == "d" || sel == "D" {
			scheduleRm(s.Name)
			schedSelection = -1
		} else if sel == "\r" || sel == "\n" {
			// View details
			detailSchedule = s.Name
			screen = "detail"
			schedSelection = -1
			first = true
		}
	} else {
		// Past run selected
		runIdx := schedSelection - len(schedules)
		if runIdx < len(allRunsList) {
			r := allRunsList[runIdx]
			if sel == "\r" || sel == "\n" {
				// View log — find and display session log
				logPath := findSessionLog(r.Pointer.CWD, r.Pointer.UUID)
				if logPath != "" {
					term.Restore(fd, oldState)
					fmt.Print("\033[?25h")
					viewCmd := exec.Command("less", logPath)
					viewCmd.Stdin = os.Stdin
					viewCmd.Stdout = os.Stdout
					viewCmd.Stderr = os.Stderr
					viewCmd.Run()
					oldState, _ = term.MakeRaw(fd)
					fmt.Print("\033[?25l")
					first = true
				}
			} else if sel == "d" || sel == "D" {
				os.Remove(filepath.Join(runsDir(), r.Filename))
				schedSelection = -1
			}
		}
	}
```

- [ ] **Step 6: Implement detail view screen**

In the `screen == "detail"` block:

```go
} else if screen == "detail" {
	s := getScheduleFromCrontab(detailSchedule)
	if s == nil {
		screen = "schedules"
		first = true
		continue
	}
	runs := listRuns(detailSchedule)

	detailKeys := activeKeys(map[byte]bool{' ': true, 'e': true, 'd': true})

	var buf strings.Builder
	desc := cronDescribe(s.Cron)
	if s.Once {
		desc = "once"
	}
	status := "enabled"
	if s.Disabled {
		status = "disabled"
	}
	dir := s.CWD
	if strings.HasPrefix(dir, repoPrefix) {
		dir = dir[len(repoPrefix):]
	}

	// Read first line of prompt
	promptLine := ""
	if data, err := os.ReadFile(promptPath(detailSchedule)); err == nil {
		lines := strings.SplitN(string(data), "\n", 2)
		promptLine = strings.TrimSpace(lines[0])
		if len(promptLine) > 60 {
			promptLine = promptLine[:57] + "..."
		}
		if len(lines) > 1 && strings.TrimSpace(lines[1]) != "" {
			promptLine += "..."
		}
	}

	buf.WriteString(fmt.Sprintf("  \033[1;37m%s\033[0m\033[K\r\n", s.Name))
	buf.WriteString(fmt.Sprintf("    Cron:      %s (%s)\033[K\r\n", s.Cron, desc))
	buf.WriteString(fmt.Sprintf("    Dir:       %s\033[K\r\n", dir))
	buf.WriteString(fmt.Sprintf("    Launcher:  %s\033[K\r\n", s.Launcher))
	exitStr := "no"
	if s.ExitOnFinish {
		exitStr = "yes"
	}
	buf.WriteString(fmt.Sprintf("    Exit:      %s\033[K\r\n", exitStr))
	buf.WriteString(fmt.Sprintf("    Status:    %s\033[K\r\n", status))
	buf.WriteString(fmt.Sprintf("    Prompt:    %s\033[K\r\n", promptLine))
	buf.WriteString("\033[K\r\n")

	buf.WriteString("  \033[1;37mRun History\033[0m\033[K\r\n")
	if len(runs) == 0 {
		buf.WriteString("    No runs yet\033[K\r\n")
	}
	for i, r := range runs {
		ts := formatRunTimestamp(r.Timestamp)
		statusIcon := "\033[1;32m✓\033[0m"
		if r.Pointer.Status == "error" {
			statusIcon = "\033[1;31m✗\033[0m"
		} else if r.Pointer.Status == "running" {
			statusIcon = "\033[1;33m⟳\033[0m"
		}
		tokens := "-"
		logPath := findSessionLog(r.Pointer.CWD, r.Pointer.UUID)
		if logPath != "" {
			t := sessionTokens(logPath)
			if t > 0 {
				tokens = fmtTokens(t)
			}
		}
		selMark := "  "
		if schedSelection == i {
			selMark = "\033[1;37m> \033[0m"
		}
		buf.WriteString(fmt.Sprintf("  %s\033[1;37m%s)\033[0m %s %s  %-8s  %s tokens\033[K\r\n",
			selMark, keyLabel(i, detailKeys), statusIcon, ts, r.Pointer.Status, tokens))
	}

	buf.WriteString("\033[K\r\n")
	if schedSelection >= 0 && schedSelection < len(runs) {
		buf.WriteString("  \033[0;90m[enter] view log  [esc] deselect\033[0m\033[K")
	} else {
		buf.WriteString("  \033[0;90m[space] enable/disable  [e]dit  [d]elete schedule  [esc] back\033[0m\033[K")
	}

	if first {
		fmt.Print("\033[2J\033[H")
		first = false
	} else {
		fmt.Print("\033[H")
	}
	fmt.Print(buf.String())
	fmt.Print("\033[J")

	sel, ok := readSel(500 * time.Millisecond)
	if !ok {
		continue
	}

	if sel == "\x1b" {
		if schedSelection >= 0 {
			schedSelection = -1
		} else {
			screen = "schedules"
			schedSelection = -1
			first = true
		}
		continue
	}

	if schedSelection >= 0 && schedSelection < len(runs) {
		// Run selected
		r := runs[schedSelection]
		if sel == "\r" || sel == "\n" {
			logPath := findSessionLog(r.Pointer.CWD, r.Pointer.UUID)
			if logPath != "" {
				term.Restore(fd, oldState)
				fmt.Print("\033[?25h")
				viewCmd := exec.Command("less", logPath)
				viewCmd.Stdin = os.Stdin
				viewCmd.Stdout = os.Stdout
				viewCmd.Stderr = os.Stderr
				viewCmd.Run()
				oldState, _ = term.MakeRaw(fd)
				fmt.Print("\033[?25l")
				first = true
			}
			schedSelection = -1
		}
	} else {
		// No run selected — handle schedule-level commands or run selection
		if sel == " " {
			if s.Disabled {
				enableScheduleInCrontab(s.Name)
			} else {
				disableScheduleInCrontab(s.Name)
			}
		} else if sel == "e" || sel == "E" {
			term.Restore(fd, oldState)
			fmt.Print("\033[?25h\r\n\r\n")
			scheduleEdit(detailSchedule)
			fmt.Print("\r\n  Press any key to continue...")
			term.MakeRaw(fd)
			readByte()
			term.Restore(fd, oldState)
			oldState, _ = term.MakeRaw(fd)
			fmt.Print("\033[?25l")
			first = true
		} else if sel == "d" || sel == "D" {
			scheduleRm(detailSchedule)
			screen = "schedules"
			first = true
		} else {
			idx, valid := keyIdx(sel, detailKeys)
			if valid && idx < len(runs) {
				schedSelection = idx
			}
		}
	}
```

- [ ] **Step 7: Verify compilation**

Run: `cd go && go build -o lila .`
Expected: Compiles without errors

- [ ] **Step 8: Run all tests**

Run: `cd go && go test -v`
Expected: All tests pass

- [ ] **Step 9: Manual smoke test**

Run the following to verify basic functionality:

```bash
# List schedules (should show none)
cd go && ./lila schedule list

# Check help
./lila schedule

# Verify TUI launches (press 'r' to see schedules screen, esc to go back, esc to quit)
./lila
```

- [ ] **Step 10: Commit**

```bash
cd go && git add tui.go && git commit -m "feat: add TUI schedules screen with selection mode and detail view"
```

---

## Final Checklist

- [ ] All tests pass: `cd go && go test -v`
- [ ] Binary compiles cleanly: `cd go && go build -o lila .`
- [ ] `lila schedule list` works
- [ ] `lila schedule add test-schedule` interactive flow works
- [ ] `lila schedule rm test-schedule` cleans up
- [ ] `lila schedule enable/disable` toggles correctly
- [ ] TUI `[r]` shows schedules screen
- [ ] TUI selection mode works with command palettes
- [ ] TUI detail view shows schedule info and run history
- [ ] Daemon detects sched-* session endings (verify by checking pointer file status after a test run completes)
