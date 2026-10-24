package agy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeAgy(t *testing.T, output string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	script := `#!/bin/bash
d="$(dirname "$0")"
printf '%s|' "$PWD" >> "$d/log"
printf '%s\x1f' "$@" >> "$d/log"
printf '\n' >> "$d/log"
cat << 'EOF'
` + output + `
EOF
`
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestLaunch(t *testing.T) {
	output := "Starting session...\n<conveyor>\nstatus: done\nsummary: Task completed\n</conveyor>\n"
	home := t.TempDir()
	cwd := t.TempDir()
	fakeAgy(t, output)

	bgID, _, err := Launch(context.Background(), home, LaunchSpec{
		Cwd:     cwd,
		Prompt:  "fix the bug",
		Name:    "CV-1",
		Bypass:  true,
		Model:   "gemini-2.5-pro",
		AddDirs: []string{"/tmp/other"},
	})
	if err != nil {
		t.Fatalf("Launch failed: %v", err)
	}
	if bgID == "" {
		t.Fatal("expected non-empty bgID (PID)")
	}

	// Verify log file was created and wait briefly for async process output
	logPath := bgLogPath(home, "CV-1")
	var b []byte
	for i := 0; i < 20; i++ {
		time.Sleep(20 * time.Millisecond)
		b, _ = os.ReadFile(logPath)
		if strings.Contains(string(b), "<conveyor>") {
			break
		}
	}
	if !strings.Contains(string(b), "<conveyor>") {
		t.Errorf("expected log file to contain <conveyor>, got: %s", string(b))
	}
}

func TestCommands(t *testing.T) {
	if cmd := AttachCommand("12345"); !strings.Contains(cmd, "12345") {
		t.Errorf("AttachCommand unexpected: %s", cmd)
	}
	if cmd := ResumeCommand("conv-uuid-1"); !strings.Contains(cmd, "conv-uuid-1") {
		t.Errorf("ResumeCommand unexpected: %s", cmd)
	}
	if cmd := FreshCommand("/path/to/brief.md", []string{"/extra/dir"}); !strings.Contains(cmd, "brief.md") || !strings.Contains(cmd, "--add-dir") {
		t.Errorf("FreshCommand unexpected: %s", cmd)
	}
}

func TestWriteSettings(t *testing.T) {
	home := t.TempDir()
	path, err := WriteSettings(home, "/usr/local/bin/conveyor")
	if err != nil {
		t.Fatalf("WriteSettings: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}

	hooksPath := filepath.Join(home, "agy-hooks.json")
	b, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("read agy-hooks.json: %v", err)
	}
	if !strings.Contains(string(b), "conveyor") {
		t.Errorf("expected hooks file to mention conveyor: %s", string(b))
	}
}

func TestExtractConversationID(t *testing.T) {
	text := "Some log line\nConversation ID: 12345678-1234-1234-1234-123456789abc\nAnother line"
	id := extractConversationID(text)
	if id != "12345678-1234-1234-1234-123456789abc" {
		t.Errorf("extractConversationID = %q, want %q", id, "12345678-1234-1234-1234-123456789abc")
	}
}
