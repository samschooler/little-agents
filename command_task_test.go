package main

import (
	"os"
	"strings"
	"testing"
)

func TestTaskConfigRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	in := &TaskConfig{
		Command: "restic backup /data",
		OnFailure: FailurePolicy{
			Retry:   &RetryPolicy{Count: 2, BackoffSeconds: 30},
			Handler: &HandlerPolicy{Enabled: true, Launcher: "claude"},
		},
	}
	if err := saveTaskConfig("job", in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := loadTaskConfig("job")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if out.Command != in.Command || out.OnFailure.Retry.Count != 2 ||
		out.OnFailure.Handler == nil || !out.OnFailure.Handler.Enabled {
		t.Fatalf("roundtrip mismatch: %+v", out)
	}
}

func TestLoadTaskConfigEmptyCommand(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	saveTaskConfig("bad", &TaskConfig{Command: "   "})
	if _, err := loadTaskConfig("bad"); err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestGenerateWrapperContents(t *testing.T) {
	w := generateWrapper("python3 redeem.py", "/s/run.log", "/usr/local/bin/lila", "/s/run.json", 3, 30)
	for _, want := range []string{
		"python3 redeem.py",       // the command, verbatim
		"code=${PIPESTATUS[0]}",   // real exit, not tee's
		"MAX=3",                   // 1 + retry.count
		"DELAY=30",                // backoff
		"record-result --pointer", // reports back to lila
		`tee -a "$LOG"`,           // dual output: pane + log
	} {
		if !strings.Contains(w, want) {
			t.Errorf("wrapper missing %q", want)
		}
	}
}

// makeCommandPointer writes a "running" command pointer and returns its path.
func makeCommandPointer(t *testing.T, name string) string {
	t.Helper()
	os.MkdirAll(runsDir(), 0755)
	path := runsDir() + "/" + name + "-20260711T120000.json"
	if err := saveRunPointer(path, RunPointer{
		UUID: "u", CWD: "/tmp", Status: "running", Kind: "command",
		Command: "false", LogPath: "/dev/null",
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApplyResultSuccess(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p := makeCommandPointer(t, "ok")
	applyResult(p, 0, 1, false)
	ptr, _ := loadRunPointer(p)
	if ptr.Status != "success" || ptr.ExitCode == nil || *ptr.ExitCode != 0 || ptr.Attempts != 1 {
		t.Fatalf("bad success pointer: %+v", ptr)
	}
	if ptr.FinishedAt == "" {
		t.Error("FinishedAt not set")
	}
}

func TestApplyResultLogOnly(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// No task config on disk => no handler => log-only.
	p := makeCommandPointer(t, "logonly")
	applyResult(p, 1, 3, false)
	ptr, _ := loadRunPointer(p)
	if ptr.Status != "failed" || *ptr.ExitCode != 1 || ptr.Attempts != 3 {
		t.Fatalf("bad failed pointer: %+v", ptr)
	}
	if ptr.HandlerUUID != "" {
		t.Errorf("log-only run should not dispatch a handler, got %q", ptr.HandlerUUID)
	}
}

func TestApplyResultIdempotent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p := makeCommandPointer(t, "idem")
	applyResult(p, 0, 1, false) // -> success
	applyResult(p, 1, 9, false) // should be a no-op (already not running)
	ptr, _ := loadRunPointer(p)
	if ptr.Status != "success" || *ptr.ExitCode != 0 || ptr.Attempts != 1 {
		t.Fatalf("second applyResult mutated a finished run: %+v", ptr)
	}
}

func TestApplyResultCrashed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p := makeCommandPointer(t, "crash")
	applyResult(p, -1, 0, true)
	ptr, _ := loadRunPointer(p)
	if ptr.Status != "crashed" {
		t.Fatalf("expected crashed, got %q", ptr.Status)
	}
}
