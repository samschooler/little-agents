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

	uuid := newUUID()

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

	if once {
		removeScheduleFromCrontab(name)
	}

	_, err = writeRunPointer(name, uuid, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run-scheduled: cannot write run pointer: %v\n", err)
		os.Exit(1)
	}

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
		cmd := fmt.Sprintf("%s --dangerously-skip-permissions --session-id %s", launcher, uuid)
		if err := tmuxNewSession(sessionName, cwd, cmd); err != nil {
			fmt.Fprintf(os.Stderr, "run-scheduled: cannot create tmux session: %v\n", err)
			os.Exit(1)
		}
		time.Sleep(2 * time.Second)
		exec.Command("tmux", "send-keys", "-t", sessionName, promptText, "Enter").Run()
	}

	fmt.Printf("run-scheduled: launched session %s (uuid: %s)\n", sessionName, uuid)
}

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
		if err := scheduleRm(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
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
	if s := getScheduleFromCrontab(name); s != nil {
		fmt.Fprintf(os.Stderr, "Error: schedule %q already exists\n", name)
		os.Exit(1)
	}

	reader := bufio.NewReader(os.Stdin)

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

	fmt.Print("  Exit when done? [y/n] (default: y): ")
	exitStr, _ := reader.ReadString('\n')
	exitStr = strings.TrimSpace(exitStr)
	exitOnFinish := exitStr == "" || exitStr == "y" || exitStr == "Y"

	pDir := promptsDir()
	os.MkdirAll(pDir, 0755)
	pPath := promptPath(name)
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
	promptData, _ := os.ReadFile(pPath)
	if strings.TrimSpace(string(promptData)) == "" {
		fmt.Fprintln(os.Stderr, "Error: prompt is empty, aborting")
		os.Remove(pPath)
		os.Exit(1)
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot find executable path: %v\n", err)
		os.Exit(1)
	}
	exe, _ = filepath.EvalSymlinks(exe)

	s := Schedule{
		Name: name, Cron: cronExpr, CWD: cwd, Launcher: launcher,
		ExitOnFinish: exitOnFinish, Once: once, BinaryPath: exe,
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

func scheduleRm(name string) error {
	if err := removeScheduleFromCrontab(name); err != nil {
		return err
	}
	os.Remove(promptPath(name))
	runs := listRuns(name)
	for _, r := range runs {
		os.Remove(filepath.Join(runsDir(), r.Filename))
	}
	fmt.Printf("  Removed schedule %q\n", name)
	return nil
}

func scheduleEdit(name string) {
	existing := getScheduleFromCrontab(name)
	if existing == nil {
		fmt.Fprintf(os.Stderr, "Error: schedule %q not found\n", name)
		os.Exit(1)
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("  Cron expression [%s] (%s): ", existing.Cron, cronDescribe(existing.Cron))
	cronExpr, _ := reader.ReadString('\n')
	cronExpr = strings.TrimSpace(cronExpr)
	if cronExpr == "" {
		cronExpr = existing.Cron
	} else {
		fmt.Printf("  → %s\n", cronDescribe(cronExpr))
	}

	fmt.Printf("  Working directory [%s]: ", existing.CWD)
	cwd, _ := reader.ReadString('\n')
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = existing.CWD
	}

	fmt.Printf("  Launcher [%s]: ", existing.Launcher)
	launcher, _ := reader.ReadString('\n')
	launcher = strings.TrimSpace(launcher)
	if launcher == "" {
		launcher = existing.Launcher
	}

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

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nano"
	}
	fmt.Printf("  Edit prompt in %s? [y/n] (default: n): ", editor)
	editPromptStr, _ := reader.ReadString('\n')
	if strings.TrimSpace(editPromptStr) == "y" || strings.TrimSpace(editPromptStr) == "Y" {
		cmd := exec.Command(editor, promptPath(name))
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
	}

	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)

	s := Schedule{
		Name: name, Cron: cronExpr, CWD: cwd, Launcher: launcher,
		ExitOnFinish: exitOnFinish, Once: existing.Once,
		Disabled: existing.Disabled, BinaryPath: exe,
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

func formatRunTimestamp(ts string) string {
	t, err := time.Parse("20060102T150405", ts)
	if err != nil {
		return ts
	}
	return t.Format("2006-01-02 15:04")
}
