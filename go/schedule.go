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
		base := strings.TrimSuffix(e.Name(), ".json")
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

func runNameFromFilename(filename string) string {
	base := strings.TrimSuffix(filename, ".json")
	if len(base) < 16 {
		return base
	}
	return base[:len(base)-16]
}

func readCrontab() (string, error) {
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
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
