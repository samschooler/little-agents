package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const daemonPollInterval = 2 * time.Second

// isDaemonRunning checks if the lila daemon service is active.
func isDaemonRunning() bool {
	switch runtime.GOOS {
	case "linux":
		out, err := exec.Command("systemctl", "--user", "is-active", systemdUnit).Output()
		return err == nil && strings.TrimSpace(string(out)) == "active"
	case "darwin":
		err := exec.Command("launchctl", "list", launchdLabel).Run()
		return err == nil
	}
	return false
}

// sessionState tracks status for a session.
type sessionState struct {
	Status string
}

// runDaemon runs the background unread-detection loop.
// Polls tmux sessions, tracks status transitions and client detach,
// and marks sessions as unread appropriately.
func runDaemon() {
	fmt.Println("lila daemon: starting unread detection")

	prevStates := make(map[string]sessionState)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(daemonPollInterval)
	defer ticker.Stop()

	// Run immediately on start, then on tick
	checkUnread(prevStates)

	for {
		select {
		case <-ticker.C:
			checkUnread(prevStates)
		case s := <-sig:
			fmt.Printf("lila daemon: caught %s, exiting\n", s)
			return
		}
	}
}

func checkUnread(prevStates map[string]sessionState) {
	sessions := listSessions()

	// Track which sessions still exist to clean up stale state
	alive := make(map[string]bool)

	for _, s := range sessions {
		alive[s.Name] = true

		if s.Command != "claude" {
			continue
		}

		curStatus := ""
		if data, err := os.ReadFile(fmt.Sprintf("/tmp/claude-status-%s", s.Name)); err == nil {
			curStatus = strings.TrimSpace(string(data))
		}

		prev, hasPrev := prevStates[s.Name]
		prevStates[s.Name] = sessionState{Status: curStatus}

		if !hasPrev {
			// First time seeing this session — just record, don't trigger
			continue
		}

		prevIdle := prev.Status == "waiting" || prev.Status == ""
		nowIdle := curStatus == "waiting" || curStatus == ""
		wasWorking := !prevIdle && prev.Status != ""

		// Working → idle: mark unread (always, even if someone is attached)
		if wasWorking && nowIdle {
			os.WriteFile(fmt.Sprintf("/tmp/claude-unread-%s", s.Name), []byte{}, 0644)
		}

		// Idle → working: someone responded, clear unread
		if prevIdle && !nowIdle {
			clearUnread(s.Name)
		}
	}

	// Clean up prevStates for dead sessions
	for name := range prevStates {
		if !alive[name] {
			delete(prevStates, name)
		}
	}

	// Check for completed scheduled runs
	checkSchedRuns(alive)
}

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
			info, err := os.Stat(logPath)
			if err == nil && info.Size() > 0 {
				status = "exited"
			}
		}
		updateRunPointerStatus(filepath.Join(dir, e.Name()), status)
	}
}

// installService writes and enables the appropriate service file
// for the current OS (systemd on Linux, launchd on Mac).
func installService() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find executable path: %w", err)
	}
	// Resolve symlinks to get the real path
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("cannot resolve executable path: %w", err)
	}

	switch runtime.GOOS {
	case "linux":
		return installSystemd(exe)
	case "darwin":
		return installLaunchd(exe)
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// uninstallService removes and disables the service.
func uninstallService() error {
	switch runtime.GOOS {
	case "linux":
		return uninstallSystemd()
	case "darwin":
		return uninstallLaunchd()
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// --- systemd ---

const systemdUnit = "lila-daemon.service"

func systemdDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

func systemdPath() string {
	return filepath.Join(systemdDir(), systemdUnit)
}

func installSystemd(exe string) error {
	unit := fmt.Sprintf(`[Unit]
Description=Little Agents Daemon - unread detection for Claude Code sessions
After=default.target

[Service]
Type=simple
ExecStart=%s daemon
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, exe)

	dir := systemdDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("cannot create systemd dir: %w", err)
	}
	if err := os.WriteFile(systemdPath(), []byte(unit), 0644); err != nil {
		return fmt.Errorf("cannot write unit file: %w", err)
	}

	// Reload and enable
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("daemon-reload failed: %w", err)
	}
	if err := exec.Command("systemctl", "--user", "enable", "--now", systemdUnit).Run(); err != nil {
		return fmt.Errorf("enable failed: %w", err)
	}

	fmt.Printf("  Installed %s\n", systemdPath())
	fmt.Printf("  Service enabled and started.\n")
	fmt.Printf("  Status: systemctl --user status %s\n", systemdUnit)
	fmt.Printf("  Logs:   journalctl --user -u %s -f\n", systemdUnit)
	return nil
}

func uninstallSystemd() error {
	_ = exec.Command("systemctl", "--user", "disable", "--now", systemdUnit).Run()
	path := systemdPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove unit file: %w", err)
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	fmt.Println("  Service stopped and removed.")
	return nil
}

// --- launchd ---

const launchdLabel = "com.littleagents.daemon"

func launchdPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
}

func installLaunchd(exe string) error {
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>daemon</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/lila-daemon.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/lila-daemon.log</string>
</dict>
</plist>
`, launchdLabel, exe)

	dir := filepath.Dir(launchdPath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("cannot create LaunchAgents dir: %w", err)
	}
	if err := os.WriteFile(launchdPath(), []byte(plist), 0644); err != nil {
		return fmt.Errorf("cannot write plist: %w", err)
	}

	if err := exec.Command("launchctl", "load", launchdPath()).Run(); err != nil {
		return fmt.Errorf("launchctl load failed: %w", err)
	}

	fmt.Printf("  Installed %s\n", launchdPath())
	fmt.Printf("  Service loaded and running.\n")
	fmt.Printf("  Logs: tail -f /tmp/lila-daemon.log\n")
	return nil
}

func uninstallLaunchd() error {
	_ = exec.Command("launchctl", "unload", launchdPath()).Run()
	path := launchdPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove plist: %w", err)
	}
	fmt.Println("  Service stopped and removed.")
	return nil
}
