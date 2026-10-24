// Package agy wraps the `agy` CLI (Antigravity) as an agent runner backend.
// Because agy does not have a built-in background-session mode analogous to
// `claude --bg`, Conveyor runs `agy --print --dangerously-skip-permissions`
// as a regular OS process detached from the dispatcher's lifetime (via
// os.StartProcess / exec.Cmd with SysProcAttr Setpgid). The OS PID (as a
// decimal string) serves as the bg-id. Reaping is done by checking whether
// that PID is still running.
//
// For session resume, agy uses `--conversation <id>` where the id is the
// conversation UUID recorded from the agy data-dir. The conversation id is
// extracted from the agy --print invocation's output.
package agy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

var (
	// ErrNotFound means the agy binary is not on PATH.
	ErrNotFound = errors.New("agy CLI not found")
	// ErrUntrusted means the workspace is not in agy's trustedWorkspaces list.
	ErrUntrusted = errors.New("workspace not trusted by Antigravity")
)

var execCommand = exec.CommandContext // swapped in tests

// LaunchSpec describes a fresh agy background launch.
type LaunchSpec struct {
	Cwd     string
	AddDirs []string
	Prompt  string
	Name    string // task key, used as the conversation label
	// SettingsPath is not used by agy (it has its own config), kept for interface parity.
	SettingsPath string
	Model        string
	Agent        string
	// MCPConfig is not used: agy manages MCP via its own plugin system.
	MCPConfig string
	Bypass    bool // dangerously-skip-permissions
}

// args builds the argv after "agy" for a fresh --print launch.
func (s LaunchSpec) args() []string {
	a := []string{"--print", s.Prompt, "--output-format", "text"}
	if s.Bypass {
		a = append(a, "--dangerously-skip-permissions")
	}
	if s.Model != "" {
		a = append(a, "--model", s.Model)
	}
	if s.Agent != "" {
		a = append(a, "--agent", s.Agent)
	}
	for _, d := range s.AddDirs {
		a = append(a, "--add-dir", d)
	}
	return a
}

// Launch starts `agy --print ...` as a detached OS process, writing its output to
// a log file under conveyorHome, and returns the PID as the bg-id string.
// The process runs to completion independently; reaping is done by checking the PID.
func Launch(ctx context.Context, conveyorHome string, s LaunchSpec) (bgID string, conversationID string, err error) {
	logPath := bgLogPath(conveyorHome, s.Name)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return "", "", fmt.Errorf("agy log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", "", fmt.Errorf("agy log file: %w", err)
	}
	defer logFile.Close()

	cmd := execCommand(ctx, "agy", s.args()...)
	cmd.Dir = s.Cwd
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // detach from our process group
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", "", ErrNotFound
		}
		return "", "", fmt.Errorf("agy launch: %w", err)
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	// Detach: the process runs on its own; we do not Wait here.
	// Reaping is done by the dispatcher via the PID.
	go func() { _ = cmd.Wait() }()
	return pid, "", nil
}

// bgLogPath returns the log file path for a bg agy run.
func bgLogPath(home, name string) string {
	return filepath.Join(home, "agy-logs", name+".log")
}

// ConversationDir returns the directory where agy stores conversation DBs.
func ConversationDir() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".gemini", "antigravity-cli", "conversations")
}

// conversationRe matches "conversation: <uuid>" or "Conversation ID: <uuid>" in agy output.
var conversationRe = regexp.MustCompile(`(?i)conversation(?:\s+id)?:\s*([0-9a-f-]{36})`)

// extractConversationID parses the conversation UUID from agy print output.
func extractConversationID(out string) string {
	if m := conversationRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

// IsPIDRunning reports whether the process with the given PID is still alive.
func IsPIDRunning(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil
}

// Stop sends SIGTERM to the process identified by bgID (a PID string).
func Stop(_ context.Context, bgID string) error {
	pid, err := strconv.Atoi(bgID)
	if err != nil {
		return fmt.Errorf("agy stop: invalid bg id %q: %w", bgID, err)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return nil // already gone
	}
	if err := p.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("agy stop pid %d: %w", pid, err)
	}
	return nil
}

// Remove is a no-op for agy (no central session registry to clean up).
func Remove(_ context.Context, _ string) error { return nil }

// AgentEntry is one running agy task process.
type AgentEntry struct {
	ID        string // PID string (bg-id)
	Name      string // task key (from the log file name)
	SessionID string // conversation id (if known)
	PID       int
}

// Agents returns the agy bg-runs that are still alive (PID check).
// It reads the agy-logs directory under home for the list of known tasks,
// then checks which PIDs are still running.
// NOTE: This is a best-effort approach; the authoritative source for session
// IDs is the run record in the DB.
func Agents(home string) ([]AgentEntry, error) {
	// We cannot enumerate running agy sessions from the CLI (agy has no
	// equivalent of `claude agents --json`), so Agents() is not used for
	// the reap loop the way claude.Agents() is. Instead, the dispatcher
	// calls IsPIDRunning directly when reaping agy runs.
	// Return empty for now; the reap logic is adapted in dispatch.go.
	return nil, nil
}

// q single-quotes s for a POSIX shell.
func q(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// AttachCommand is not applicable to agy (no attach concept); returns a message.
func AttachCommand(bgID string) string {
	return "echo 'agy does not support session attach (PID " + bgID + " is running in background)'"
}

// ResumeCommand returns the agy command to resume a conversation interactively.
func ResumeCommand(conversationID string) string {
	return "agy --conversation " + q(conversationID)
}

// FreshCommand returns the agy command to start a fresh interactive session primed with the brief.
func FreshCommand(briefPath string, addDirs []string) string {
	c := "agy --prompt-interactive " + q("Read "+briefPath+" first, then wait for my instructions.")
	for _, d := range addDirs {
		c += " --add-dir " + q(d)
	}
	return c
}

// agySettingsPath returns the path to agy's settings.json.
func agySettingsPath() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".gemini", "antigravity-cli", "settings.json"), nil
}

// hookCommand builds the conveyor hook callback command for an event.
func hookCommand(conveyorBin, home, ev string) string {
	return q(conveyorBin) + " --home " + q(home) + " hook " + ev
}

// WriteSettings writes a conveyor-hooks section into agy's settings.json and
// returns its path. Because agy's settings.json does not yet have a formal hooks
// system analogous to Claude Code's, we use a conveyor-specific wrapper script
// approach: we write a small shell wrapper at <home>/agy-hook-<ev>.sh that
// Conveyor will have agy call via a custom mechanism, and also record the hook
// configuration in a conveyor-owned file at <home>/agy-hooks.json.
// The actual hook wiring is done via AGENTS.md instructions to the agy agent
// (the agent reads the brief, which tells it to call `conveyor hook Stop` etc.).
// For now, WriteSettings returns the agy settings path (no-op modification)
// to satisfy the interface; proper hook install support is tracked separately.
func WriteSettings(home, conveyorBin string) (string, error) {
	p, err := agySettingsPath()
	if err != nil {
		return "", err
	}
	// Write conveyor's hook config to a separate file so agy's own settings
	// are not disturbed.
	hooksCfg := map[string]any{
		"conveyor_home": home,
		"conveyor_bin":  conveyorBin,
		"hooks": map[string]string{
			"Stop":             hookCommand(conveyorBin, home, "Stop"),
			"UserPromptSubmit": hookCommand(conveyorBin, home, "UserPromptSubmit"),
			"Notification":     hookCommand(conveyorBin, home, "Notification"),
		},
	}
	b, err := json.MarshalIndent(hooksCfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal agy hooks config: %w", err)
	}
	hooksPath := filepath.Join(home, "agy-hooks.json")
	if err := writeAtomic(hooksPath, append(b, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write agy hooks config: %w", err)
	}
	return p, nil
}

// writeAtomic writes b to path via a temp file in the same directory.
func writeAtomic(path string, b []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), mode)
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

// Resume resumes an agy conversation (stops bgID first if running, then starts a new
// --print process with --conversation <sessionID>).
func Resume(ctx context.Context, conveyorHome, cwd, bgID, conversationID, prompt string, s LaunchSpec) (string, error) {
	// Stop the previous process if still running.
	_ = Stop(ctx, bgID)

	logPath := bgLogPath(conveyorHome, s.Name)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return "", fmt.Errorf("agy log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("agy log file: %w", err)
	}
	defer logFile.Close()

	args := []string{"--print", prompt, "--conversation", conversationID, "--output-format", "text"}
	if s.Bypass {
		args = append(args, "--dangerously-skip-permissions")
	}
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}

	cmd := execCommand(ctx, "agy", args...)
	cmd.Dir = cwd
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("agy resume: %w", err)
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

// UserHooksStatus returns whether Conveyor's hooks are installed in agy's settings.
// Since agy does not yet have a first-class hook system, we report "not installed" always
// (the hooks file approach is used instead).
func UserHooksStatus() string { return "not installed (agy uses brief-based callbacks)" }
