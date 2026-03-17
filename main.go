package main

import (
	"fmt"
	"os"
)

func main() {
	// Subcommands
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "daemon":
			runDaemon()
			return
		case "install-service":
			if err := installService(); err != nil {
				fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "uninstall-service":
			if err := uninstallService(); err != nil {
				fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "schedule":
			scheduleCmd(os.Args[2:])
			return
		case "run-scheduled":
			runScheduledCmd(os.Args[2:])
			return
		}
	}

	// Flags
	promptMe := false
	for _, arg := range os.Args[1:] {
		if arg == "--prompt-me" {
			promptMe = true
		}
		if arg == "--quota" {
			q := getQuota()
			fmt.Printf("%s %d %s\n", q.Formatted, q.Pct, q.ResetStr)
			return
		}
		if arg == "--help" || arg == "-h" {
			printHelp()
			return
		}
	}

	runTUI(promptMe)
}

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
