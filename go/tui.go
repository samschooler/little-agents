package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const allKeys = "qwertyuiopasdfghjlzxcvbnm"

type sessionInfo struct {
	Name    string
	PanePID string
	Command string
	CWD     string
}

// activeKeys returns the key string with reserved keys removed.
func activeKeys(reserved map[byte]bool) string {
	if len(reserved) == 0 {
		return allKeys
	}
	var b strings.Builder
	for i := 0; i < len(allKeys); i++ {
		if !reserved[allKeys[i]] {
			b.WriteByte(allKeys[i])
		}
	}
	return b.String()
}

func keyLabel(i int, keys string) string {
	klen := len(keys)
	p := i / klen
	l := string(keys[i%klen])
	if p == 0 {
		return l
	}
	return fmt.Sprintf("%d%s", p, l)
}

func keyIdx(input string, keys string) (int, bool) {
	klen := len(keys)
	p := 0
	l := input
	if len(input) == 2 {
		p = int(input[0] - '0')
		l = string(input[1])
	}
	for i := 0; i < klen; i++ {
		if string(keys[i]) == l {
			return p*klen + i, true
		}
	}
	return 0, false
}

func stateDir() string {
	xdg := os.Getenv("XDG_STATE_HOME")
	if xdg == "" {
		home, _ := os.UserHomeDir()
		xdg = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(xdg, "little-agents")
}

func getLauncher() string {
	data, err := os.ReadFile(filepath.Join(stateDir(), "launcher"))
	if err != nil {
		return "claude"
	}
	l := strings.TrimSpace(string(data))
	if l == "claude" || l == "codex" {
		return l
	}
	return "claude"
}

func setLauncher(l string) {
	dir := stateDir()
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "launcher"), []byte(l+"\n"), 0644)
}

func launcherCmd(launcher, prompt string) string {
	if launcher == "codex" {
		return "codex --dangerously-bypass-approvals-and-sandbox"
	}
	if prompt != "" {
		return fmt.Sprintf("claude --dangerously-skip-permissions %s", shellQuote(prompt))
	}
	return "claude --dangerously-skip-permissions"
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func sessionStatus(name string) string {
	data, err := os.ReadFile(fmt.Sprintf("/tmp/claude-status-%s", name))
	if err != nil {
		return "\033[1;36m◉ waiting\033[0m"
	}
	status := strings.TrimSpace(string(data))
	switch status {
	case "", "waiting":
		return "\033[1;36m◉ waiting\033[0m"
	case "thinking":
		return "\033[1;33m💭 thinking\033[0m"
	default:
		return fmt.Sprintf("\033[1;33m⚡%s\033[0m", status)
	}
}

func listSessions() []sessionInfo {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F",
		"#{session_name} #{pane_pid} #{pane_current_command} #{pane_current_path}").Output()
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var sessions []sessionInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 4)
		if len(parts) < 4 {
			continue
		}
		name := parts[0]
		if seen[name] {
			continue
		}
		seen[name] = true
		sessions = append(sessions, sessionInfo{
			Name:    name,
			PanePID: parts[1],
			Command: parts[2],
			CWD:     parts[3],
		})
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Name < sessions[j].Name
	})
	return sessions
}

func hasUnread(name string) bool {
	_, err := os.Stat(fmt.Sprintf("/tmp/claude-unread-%s", name))
	return err == nil
}

func clearUnread(name string) {
	os.Remove(fmt.Sprintf("/tmp/claude-unread-%s", name))
}

func tmuxAttach(session string) {
	cmd := exec.Command("tmux", "a", "-t", session)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
}

func tmuxKill(session string) {
	exec.Command("tmux", "kill-session", "-t", session).Run()
}

func tmuxHasClients(session string) bool {
	out, err := exec.Command("tmux", "list-clients", "-t", session).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

func tmuxNewSession(name, cwd, command string) error {
	return exec.Command("tmux", "new-session", "-d", "-s", name, "-c", cwd, command).Run()
}

func listRepos() []string {
	home, _ := os.UserHomeDir()
	entries, err := os.ReadDir(filepath.Join(home, "repo"))
	if err != nil {
		return nil
	}
	var repos []string
	for _, e := range entries {
		if e.IsDir() {
			repos = append(repos, e.Name())
		}
	}
	sort.Strings(repos)
	return repos
}

// stdinFd caches the stdin file descriptor
var stdinFd = int(os.Stdin.Fd())

// readByte reads a single byte from stdin, blocking until available.
func readByte() (byte, bool) {
	buf := make([]byte, 1)
	n, err := os.Stdin.Read(buf)
	if err != nil || n == 0 {
		return 0, false
	}
	return buf[0], true
}

// readByteTimeout reads a single byte with a timeout using poll(2).
// Returns (byte, true) if a byte was read, (0, false) on timeout.
func readByteTimeout(timeout time.Duration) (byte, bool) {
	fds := []unix.PollFd{{Fd: int32(stdinFd), Events: unix.POLLIN}}
	ms := int(timeout.Milliseconds())
	n, err := unix.Poll(fds, ms)
	if err != nil || n <= 0 {
		return 0, false
	}
	return readByte()
}

// readSel reads a selection: single letter or digit+letter (with timeout)
func readSel(timeout time.Duration) (string, bool) {
	k, ok := readByteTimeout(timeout)
	if !ok {
		return "", false
	}
	if k >= '0' && k <= '9' {
		k2, ok2 := readByteTimeout(500 * time.Millisecond)
		if !ok2 {
			return string(k), true
		}
		return string(k) + string(k2), true
	}
	return string(k), true
}

// readSelBlocking reads a selection without timeout
func readSelBlocking() (string, bool) {
	k, ok := readByte()
	if !ok {
		return "", false
	}
	if k >= '0' && k <= '9' {
		k2, ok2 := readByteTimeout(500 * time.Millisecond)
		if !ok2 {
			return string(k), true
		}
		return string(k) + string(k2), true
	}
	return string(k), true
}

func readLine() string {
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return scanner.Text()
	}
	return ""
}

func runTUI(promptMe bool) {
	fd := int(os.Stdin.Fd())

	// Save terminal state and set raw mode
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting raw mode: %v\n", err)
		return
	}
	defer term.Restore(fd, oldState)

	// Hide cursor
	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	first := true
	home, _ := os.UserHomeDir()
	repoPrefix := filepath.Join(home, "repo") + "/"
	daemonOk := isDaemonRunning()
	// Re-check daemon status every 10 cycles (~5s) to avoid calling systemctl every 0.5s
	daemonCheckCounter := 0

	screen := "main" // "main", "schedules", "detail"
	var schedSelection int = -1
	var detailSchedule string

	for {
		daemonCheckCounter++
		if daemonCheckCounter >= 10 {
			daemonOk = isDaemonRunning()
			daemonCheckCounter = 0
		}

		if screen == "main" {
			// Build active key set — reserve hotkeys that are in use
			reserved := map[byte]bool{
				'k': true, // kill
				'n': true, // new
				'c': true, // toggle cli
				'r': true, // schedules
			}
			if !daemonOk {
				reserved['s'] = true // install service
			}
			keys := activeKeys(reserved)

			launcher := getLauncher()
			q := getQuota()

			// Quota color
			qc := "\033[1;32m" // green
			if q.Pct >= 80 {
				qc = "\033[1;31m" // red
			} else if q.Pct >= 50 {
				qc = "\033[1;33m" // yellow
			}

			sessions := listSessions()

			// Build display
			var buf strings.Builder
			for i, s := range sessions {
				dot := ""
				if hasUnread(s.Name) {
					dot = " \033[1;31m●\033[0m"
				}
				st := ""
				if s.Command == "claude" {
					st = " " + sessionStatus(s.Name)
				}
				dir := s.CWD
				if strings.HasPrefix(dir, repoPrefix) {
					dir = dir[len(repoPrefix):]
				}
				buf.WriteString(fmt.Sprintf("    \033[1;37m%s)\033[0m%s %s \033[0;90m[%s]\033[0m%s\033[K\r\n",
					keyLabel(i, keys), dot, s.Name, dir, st))
			}
			if len(sessions) == 0 {
				buf.WriteString("  No active tmux sessions\033[K\r\n")
			}
			buf.WriteString("\033[K\r\n")
			buf.WriteString(fmt.Sprintf("  %s⚡%s (%d%%)\033[0m \033[0;90mresets %s\033[0m\033[K\r\n",
				qc, q.Formatted, q.Pct, q.ResetStr))
			if !daemonOk {
				buf.WriteString("  \033[1;33m⚠ Service not running. Some features won't work as expected.\033[0m\033[K\r\n")
			}
			sKey := ""
			if !daemonOk {
				sKey = "  [s] install service"
			}
			if len(sessions) > 0 {
				buf.WriteString(fmt.Sprintf("  \033[0;90m[%s-%s] attach  [k] kill  [n] new  [c] cli:%s  [r] recurring%s  [esc] quit\033[0m\033[K",
					keyLabel(0, keys), keyLabel(len(sessions)-1, keys), launcher, sKey))
			} else {
				buf.WriteString(fmt.Sprintf("  \033[0;90m[n] new  [c] cli:%s  [r] recurring%s  [esc] quit\033[0m\033[K", launcher, sKey))
			}

			if first {
				fmt.Print("\033[2J\033[H") // clear screen
				first = false
			} else {
				fmt.Print("\033[H") // cursor home
			}
			fmt.Print(buf.String())
			fmt.Print("\033[J") // clear below

			sel, ok := readSel(500 * time.Millisecond)
			if !ok {
				continue
			}

			if sel == "\x1b" { // ESC
				return
			}

			if sel == "k" || sel == "K" {
				fmt.Print("\r\n\r\n  \033[0;90mKill session [key or esc]:\033[0m ")
				sel2, ok2 := readSelBlocking()
				if !ok2 || sel2 == "\x1b" {
					continue
				}
				idx, valid := keyIdx(sel2, keys)
				if valid && idx < len(sessions) {
					clearUnread(sessions[idx].Name)
					tmuxKill(sessions[idx].Name)
				}
			} else if sel == "r" || sel == "R" {
				screen = "schedules"
				schedSelection = -1
				first = true
				continue
			} else if sel == "n" || sel == "N" {
				// Restore terminal for interactive input
				term.Restore(fd, oldState)
				fmt.Print("\033[?25h") // show cursor
				fmt.Print("\r\n\r\n  Session name: ")
				name := readLine()
				if name != "" {
					fmt.Println("  Select repo:")
					repos := listRepos()
					for i, r := range repos {
						fmt.Printf("    %s) %s\r\n", keyLabel(i, allKeys), r)
					}
					if len(repos) == 0 {
						fmt.Println("    (none)")
					}
					fmt.Println()
					if len(repos) > 0 {
						fmt.Printf("  \033[0;90m[%s-%s] select  [n] new  [esc] cancel:\033[0m ",
							keyLabel(0, allKeys), keyLabel(len(repos)-1, allKeys))
					} else {
						fmt.Print("  \033[0;90m[n] new  [esc] cancel:\033[0m ")
					}

					// Read repo selection in raw mode briefly
					term.MakeRaw(fd)
					rsel, rok := readSelBlocking()
					term.Restore(fd, oldState)

					if !rok || rsel == "\x1b" {
						// re-enter raw mode for main loop
						oldState, _ = term.MakeRaw(fd)
						fmt.Print("\033[?25l")
						continue
					}

					var selectedRepo string
					if rsel == "n" || rsel == "N" {
						fmt.Print("\r\n  New repo name: ")
						newRepo := readLine()
						if newRepo != "" {
							repoPath := filepath.Join(home, "repo", newRepo)
							os.MkdirAll(repoPath, 0755)
							selectedRepo = repoPath
						}
					} else {
						ridx, valid := keyIdx(rsel, allKeys)
						if valid && ridx < len(repos) {
							selectedRepo = filepath.Join(home, "repo", repos[ridx])
						}
					}

					if selectedRepo != "" {
						prompt := ""
						if promptMe {
							fmt.Print("\r\n  Starting prompt (enter to skip): ")
							prompt = readLine()
						}
						cmd := launcherCmd(getLauncher(), prompt)
						if err := tmuxNewSession(name, selectedRepo, cmd); err == nil {
							// Re-enter raw mode briefly to reset, then restore for attach
							oldState, _ = term.MakeRaw(fd)
							term.Restore(fd, oldState)
							tmuxAttach(name)
						}
					}
				}
				// Re-enter raw mode for main loop
				oldState, _ = term.MakeRaw(fd)
				fmt.Print("\033[?25l")
			} else if (sel == "s" || sel == "S") && !daemonOk {
				term.Restore(fd, oldState)
				fmt.Print("\033[?25h")
				fmt.Print("\r\n\r\n")
				if err := installService(); err != nil {
					fmt.Fprintf(os.Stderr, "  Error: %v\r\n", err)
				}
				daemonOk = isDaemonRunning()
				daemonCheckCounter = 0
				fmt.Print("\r\n  Press any key to continue...")
				term.MakeRaw(fd)
				readByte()
				term.Restore(fd, oldState)
				oldState, _ = term.MakeRaw(fd)
				fmt.Print("\033[?25l")
			} else if sel == "c" || sel == "C" {
				if launcher == "claude" {
					setLauncher("codex")
				} else {
					setLauncher("claude")
				}
			} else {
				idx, valid := keyIdx(sel, keys)
				if valid && idx < len(sessions) {
					// Restore terminal for tmux
					term.Restore(fd, oldState)
					fmt.Print("\033[?25h")
					clearUnread(sessions[idx].Name)
					tmuxAttach(sessions[idx].Name)
					// Clear again after detach — user just viewed this session
					clearUnread(sessions[idx].Name)
					// Re-enter raw mode
					oldState, _ = term.MakeRaw(fd)
					fmt.Print("\033[?25l")
				}
			}
		} else if screen == "schedules" {
			schedules := listSchedules()
			allRuns := listAllRuns()
			if len(allRuns) > 20 {
				allRuns = allRuns[:20]
			}

			schedKeys := activeKeys(map[byte]bool{'n': true})

			// Build items list: schedules first, then past runs
			type listItem struct {
				isSchedule bool
				schedule   Schedule
				run        RunInfo
			}
			var items []listItem
			for _, s := range schedules {
				items = append(items, listItem{isSchedule: true, schedule: s})
			}
			for _, r := range allRuns {
				items = append(items, listItem{isSchedule: false, run: r})
			}

			var buf strings.Builder
			buf.WriteString("  \033[1;37mSchedules\033[0m\033[K\r\n")
			buf.WriteString("\033[K\r\n")

			if len(schedules) == 0 {
				buf.WriteString("  No schedules configured\033[K\r\n")
			}
			for i, s := range schedules {
				desc := cronDescribe(s.Cron)
				if s.Once {
					desc = "(once)"
				}
				status := "\033[1;32menabled\033[0m"
				if s.Disabled {
					status = "\033[0;90mdisabled\033[0m"
				}
				dir := s.CWD
				if strings.HasPrefix(dir, repoPrefix) {
					dir = dir[len(repoPrefix):]
				}
				sel := "  "
				if schedSelection == i {
					sel = "\033[1;33m> \033[0m"
				}
				buf.WriteString(fmt.Sprintf("  %s\033[1;37m%s)\033[0m %s \033[0;90m%s [%s]\033[0m %s\033[K\r\n",
					sel, keyLabel(i, schedKeys), s.Name, desc, dir, status))
			}

			if len(allRuns) > 0 {
				buf.WriteString("\033[K\r\n")
				buf.WriteString("  \033[0;90mRecent runs\033[0m\033[K\r\n")
				for j, r := range allRuns {
					idx := len(schedules) + j
					name := runNameFromFilename(r.Filename)
					ts := formatRunTimestamp(r.Timestamp)
					statusColor := "\033[0;90m"
					if r.Pointer.Status == "running" {
						statusColor = "\033[1;33m"
					}
					sel := "  "
					if schedSelection == idx {
						sel = "\033[1;33m> \033[0m"
					}
					buf.WriteString(fmt.Sprintf("  %s\033[1;37m%s)\033[0m %s \033[0;90m%s\033[0m %s%s\033[0m\033[K\r\n",
						sel, keyLabel(idx, schedKeys), name, ts, statusColor, r.Pointer.Status))
				}
			}

			buf.WriteString("\033[K\r\n")
			// Context-sensitive footer
			if schedSelection >= 0 && schedSelection < len(items) {
				item := items[schedSelection]
				if item.isSchedule {
					toggleLabel := "disable"
					if item.schedule.Disabled {
						toggleLabel = "enable"
					}
					buf.WriteString(fmt.Sprintf("  \033[0;90m[space] %s  [d] delete  [enter] details  [esc] deselect\033[0m\033[K",
						toggleLabel))
				} else {
					buf.WriteString("  \033[0;90m[enter] view log  [d] delete log  [esc] deselect\033[0m\033[K")
				}
			} else {
				buf.WriteString("  \033[0;90m[a-z] select  [n] new  [esc] back\033[0m\033[K")
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

			if schedSelection >= 0 && schedSelection < len(items) {
				item := items[schedSelection]
				if sel == " " && item.isSchedule {
					if item.schedule.Disabled {
						enableScheduleInCrontab(item.schedule.Name)
					} else {
						disableScheduleInCrontab(item.schedule.Name)
					}
					first = true
					continue
				} else if sel == "d" || sel == "D" {
					if item.isSchedule {
						scheduleRm(item.schedule.Name)
					} else {
						os.Remove(filepath.Join(runsDir(), item.run.Filename))
					}
					schedSelection = -1
					first = true
					continue
				} else if sel == "\r" || sel == "\n" {
					if item.isSchedule {
						detailSchedule = item.schedule.Name
						screen = "detail"
						schedSelection = -1
						first = true
					} else {
						logPath := findSessionLog(item.run.Pointer.CWD, item.run.Pointer.UUID)
						if logPath != "" {
							term.Restore(fd, oldState)
							fmt.Print("\033[?25h")
							cmd := exec.Command("less", logPath)
							cmd.Stdin = os.Stdin
							cmd.Stdout = os.Stdout
							cmd.Stderr = os.Stderr
							cmd.Run()
							oldState, _ = term.MakeRaw(fd)
							fmt.Print("\033[?25l")
							first = true
						}
					}
					continue
				}
			}

			if sel == "n" || sel == "N" {
				term.Restore(fd, oldState)
				fmt.Print("\033[?25h")
				fmt.Print("\r\n\r\n  Schedule name: ")
				name := readLine()
				if name != "" {
					scheduleAdd(name)
				}
				oldState, _ = term.MakeRaw(fd)
				fmt.Print("\033[?25l")
				first = true
				continue
			}

			// Item selection
			idx, valid := keyIdx(sel, schedKeys)
			if valid && idx < len(items) {
				if schedSelection == idx {
					schedSelection = -1
				} else {
					schedSelection = idx
				}
			}
		} else if screen == "detail" {
			s := getScheduleFromCrontab(detailSchedule)
			if s == nil {
				screen = "schedules"
				first = true
				continue
			}

			runs := listRuns(detailSchedule)
			detailRunKeys := activeKeys(map[byte]bool{' ': true, 'e': true, 'd': true})

			var buf strings.Builder
			buf.WriteString(fmt.Sprintf("  \033[1;37m%s\033[0m\033[K\r\n", s.Name))
			buf.WriteString("\033[K\r\n")

			desc := cronDescribe(s.Cron)
			if s.Once {
				desc = "(once)"
			}
			buf.WriteString(fmt.Sprintf("  Cron:       %s (%s)\033[K\r\n", s.Cron, desc))

			dir := s.CWD
			if strings.HasPrefix(dir, repoPrefix) {
				dir = dir[len(repoPrefix):]
			}
			buf.WriteString(fmt.Sprintf("  Directory:  %s\033[K\r\n", dir))
			buf.WriteString(fmt.Sprintf("  Launcher:   %s\033[K\r\n", s.Launcher))
			exitStr := "no"
			if s.ExitOnFinish {
				exitStr = "yes"
			}
			buf.WriteString(fmt.Sprintf("  Exit done:  %s\033[K\r\n", exitStr))

			statusStr := "\033[1;32menabled\033[0m"
			if s.Disabled {
				statusStr = "\033[0;90mdisabled\033[0m"
			}
			buf.WriteString(fmt.Sprintf("  Status:     %s\033[K\r\n", statusStr))

			// Show first line of prompt (truncated)
			promptData, err := os.ReadFile(promptPath(detailSchedule))
			if err == nil {
				promptLine := strings.TrimSpace(string(promptData))
				if nl := strings.IndexByte(promptLine, '\n'); nl >= 0 {
					promptLine = promptLine[:nl]
				}
				if len(promptLine) > 60 {
					promptLine = promptLine[:57] + "..."
				}
				buf.WriteString(fmt.Sprintf("  Prompt:     \033[0;90m%s\033[0m\033[K\r\n", promptLine))
			}

			buf.WriteString("\033[K\r\n")
			if len(runs) > 0 {
				buf.WriteString("  \033[0;90mRun history\033[0m\033[K\r\n")
				for i, r := range runs {
					ts := formatRunTimestamp(r.Timestamp)
					statusColor := "\033[0;90m"
					if r.Pointer.Status == "running" {
						statusColor = "\033[1;33m"
					}
					tokens := ""
					logPath := findSessionLog(r.Pointer.CWD, r.Pointer.UUID)
					if logPath != "" {
						tokens = fmt.Sprintf(" \033[0;90m%s tokens\033[0m", fmtTokens(sessionTokens(logPath)))
					}
					sel := "  "
					if schedSelection == i {
						sel = "\033[1;33m> \033[0m"
					}
					buf.WriteString(fmt.Sprintf("  %s\033[1;37m%s)\033[0m %s %s%s\033[0m%s\033[K\r\n",
						sel, keyLabel(i, detailRunKeys), ts, statusColor, r.Pointer.Status, tokens))
				}
			} else {
				buf.WriteString("  No runs yet\033[K\r\n")
			}

			buf.WriteString("\033[K\r\n")
			// Footer
			toggleLabel := "disable"
			if s.Disabled {
				toggleLabel = "enable"
			}
			if schedSelection >= 0 && schedSelection < len(runs) {
				buf.WriteString(fmt.Sprintf("  \033[0;90m[space] %s  [e] edit  [d] delete  [enter] view log  [esc] deselect\033[0m\033[K",
					toggleLabel))
			} else {
				buf.WriteString(fmt.Sprintf("  \033[0;90m[space] %s  [e] edit  [d] delete  [esc] back\033[0m\033[K",
					toggleLabel))
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
					first = true
				}
				continue
			}

			if sel == " " {
				if s.Disabled {
					enableScheduleInCrontab(s.Name)
				} else {
					disableScheduleInCrontab(s.Name)
				}
				first = true
				continue
			}

			if sel == "e" || sel == "E" {
				term.Restore(fd, oldState)
				fmt.Print("\033[?25h")
				fmt.Print("\r\n")
				scheduleEdit(s.Name)
				oldState, _ = term.MakeRaw(fd)
				fmt.Print("\033[?25l")
				first = true
				continue
			}

			if sel == "d" || sel == "D" {
				scheduleRm(s.Name)
				screen = "schedules"
				schedSelection = -1
				first = true
				continue
			}

			if (sel == "\r" || sel == "\n") && schedSelection >= 0 && schedSelection < len(runs) {
				r := runs[schedSelection]
				logPath := findSessionLog(r.Pointer.CWD, r.Pointer.UUID)
				if logPath != "" {
					term.Restore(fd, oldState)
					fmt.Print("\033[?25h")
					cmd := exec.Command("less", logPath)
					cmd.Stdin = os.Stdin
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr
					cmd.Run()
					oldState, _ = term.MakeRaw(fd)
					fmt.Print("\033[?25l")
					first = true
				}
				continue
			}

			// Run selection
			idx, valid := keyIdx(sel, detailRunKeys)
			if valid && idx < len(runs) {
				if schedSelection == idx {
					schedSelection = -1
				} else {
					schedSelection = idx
				}
			}
		}
	}
}
