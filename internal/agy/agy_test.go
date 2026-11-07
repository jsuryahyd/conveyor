package agy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractConversationID(t *testing.T) {
	out := `Some header
Conversation ID: 12345678-1234-1234-1234-123456789abc
Model: gemini-pro`
	if got := ExtractConversationID(out); got != "12345678-1234-1234-1234-123456789abc" {
		t.Errorf("got %q", got)
	}
	out2 := "conversation: abcdef01-2345-6789-abcd-ef0123456789"
	if got := ExtractConversationID(out2); got != "abcdef01-2345-6789-abcd-ef0123456789" {
		t.Errorf("got %q", got)
	}
	if got := ExtractConversationID("no conversation here"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestWriteSettings(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteSettings(dir, "/usr/local/bin/conveyor")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "agy-settings.json") {
		t.Errorf("unexpected path: %s", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Error("settings file is empty")
	}
}

func TestFreshAndResumeCommand(t *testing.T) {
	f := FreshCommand("/path/to/agy-settings.json", "/path/to/BRIEF.md", []string{"/extra/repo"})
	if !strings.Contains(f, "agy --settings '/path/to/agy-settings.json' --dangerously-skip-permissions --prompt-interactive") {
		t.Errorf("unexpected fresh command: %s", f)
	}
	if !strings.Contains(f, "--add-dir '/extra/repo'") {
		t.Errorf("fresh command missing add-dir: %s", f)
	}

	r := ResumeCommand("conv-uuid-123", "/path/to/agy-settings.json", []string{"/extra/repo"})
	if !strings.Contains(r, "agy --settings '/path/to/agy-settings.json' --dangerously-skip-permissions --conversation 'conv-uuid-123'") {
		t.Errorf("unexpected resume command: %s", r)
	}
	if !strings.Contains(r, "--add-dir '/extra/repo'") {
		t.Errorf("resume command missing add-dir: %s", r)
	}

	att := AttachCommand("999999999")
	if strings.Contains(att, "kill -TERM") {
		t.Errorf("attach command should not kill process: %s", att)
	}
}

func TestLaunchSpecArgs(t *testing.T) {
	s := LaunchSpec{
		Prompt:       "do something",
		SettingsPath: "/path/to/agy-settings.json",
		Bypass:       true,
		Model:        "gemini-pro",
		Agent:        "test-agent",
		AddDirs:      []string{"/dir1", "/dir2"},
	}
	args := s.args()
	joined := strings.Join(args, " ")
	for _, expected := range []string{
		"--print", "do something",
		"--settings /path/to/agy-settings.json",
		"--dangerously-skip-permissions",
		"--model gemini-pro",
		"--agent test-agent",
		"--add-dir /dir1",
		"--add-dir /dir2",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("args %q does not contain %q", joined, expected)
		}
	}
}

func TestMockLaunch(t *testing.T) {
	oldExec := execCommand
	defer func() { execCommand = oldExec }()

	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	}

	home := t.TempDir()
	cwd := t.TempDir()
	pid, _, err := Launch(context.Background(), home, LaunchSpec{
		Cwd:          cwd,
		Name:         "CV-1",
		Prompt:       "test",
		SettingsPath: filepath.Join(home, "agy-settings.json"),
		Bypass:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pid == "" {
		t.Fatal("expected non-empty pid")
	}
}
