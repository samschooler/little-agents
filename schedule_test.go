package main

import (
	"strings"
	"testing"
)

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
