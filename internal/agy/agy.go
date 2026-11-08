// Package agy wraps the `agy` CLI (Antigravity) as an agent runner backend.
// Because agy does not have a built-in background-session mode analogous to
// `claude --bg`, Conveyor runs `agy --print --dangerously-skip-permissions`
// as a regular OS process detached from the dispatcher's lifetime (via
// os.StartProcess / exec.Cmd with SysProcAttr Setpgid). The OS PID (as a
// decimal string) serves as the bg-id. Reaping is done by checking whether
// that PID is still running.
//
// For session resume, agy uses `--conversation <id>` where the id is the
// conversation UUID. agy accepts session-specific configuration via the
// `--settings <settingsPath>` flag, mirroring the claude runner.
package agy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	// ErrNotFound means the agy binary is not on PATH.
	ErrNotFound = errors.New("agy CLI not found")
	// ErrUntrusted means the workspace is not trusted by Antigravity.
	ErrUntrusted = errors.New("workspace not trusted by Antigravity")
)

var execCommand = exec.CommandContext // swapped in tests

// LaunchSpec describes a fresh agy background launch.
type LaunchSpec struct {
	Cwd          string
	AddDirs      []string
	Prompt       string
	Name         string // task key, used as the conversation label and log name
	SettingsPath string // path to agy-settings.json
	Model        string
	Agent        string
	MCPConfig    string
	Bypass       bool // dangerously-skip-permissions
}

func (s LaunchSpec) args() []string {
	a := []string{"--print", s.Prompt, "--output-format", "text"}
	if s.SettingsPath != "" {
		a = append(a, "--settings", s.SettingsPath)
	}
	if s.Bypass {
		a = append(a, "--dangerously-skip-permissions")
	}
	if s.Model != "" {
		a = append(a, "--model", sanitizeAgyModel(s.Model))
	}
	if s.Agent != "" {
		ensureAgyAgentAvailable(s.Agent)
		a = append(a, "--agent", s.Agent)
	}
	for _, d := range s.AddDirs {
		a = append(a, "--add-dir", d)
	}
	return a
}

// sanitizeAgyModel maps Claude model aliases to Antigravity model identifiers so
// agy does not exit with code 1 due to unrecognized model flags.
func sanitizeAgyModel(m string) string {
	m = strings.TrimSpace(m)
	if m == "" {
		return ""
	}
	lower := strings.ToLower(m)
	switch {
	case lower == "sonnet" || strings.HasPrefix(lower, "claude-sonnet") || strings.HasPrefix(lower, "claude-3-5-sonnet") || strings.HasPrefix(lower, "claude-3-7-sonnet"):
		return "claude-sonnet-4-6"
	case lower == "opus" || strings.HasPrefix(lower, "claude-opus") || strings.HasPrefix(lower, "claude-3-opus"):
		return "claude-opus-4-6-thinking"
	case lower == "haiku" || strings.HasPrefix(lower, "claude-haiku") || strings.HasPrefix(lower, "claude-3-5-haiku"):
		return "gemini-3.8-flash-medium"
	case lower == "flash":
		return "gemini-3.8-flash-medium"
	}
	return m
}

var (
	agentModelLineRe = regexp.MustCompile(`(?m)^model:\s*(.+)$`)
	agentToolsLineRe = regexp.MustCompile(`(?m)^tools:\s*(.+)$`)
)

func mapClaudeToolsToAgy(raw string) []string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")
	parts := strings.Split(raw, ",")
	seen := make(map[string]bool)
	var out []string

	add := func(t string) {
		t = strings.TrimSpace(t)
		if !seen[t] && t != "" {
			seen[t] = true
			out = append(out, t)
		}
	}

	for _, p := range parts {
		item := strings.Trim(strings.TrimSpace(p), `"'`)
		switch strings.ToLower(item) {
		case "bash":
			add("run_command")
			add("manage_task")
		case "read", "glob", "grep":
			add("read_file")
		case "edit", "multiedit":
			add("edit_file")
		case "write":
			add("write_file")
		case "websearch", "webfetch":
			add("search_web")
		default:
			if item != "" {
				add(item)
			}
		}
	}

	if len(out) == 0 {
		return []string{"read_file", "edit_file", "write_file", "run_command", "manage_task", "search_web"}
	}
	return out
}

// sanitizeAgyAgentContent adjusts Claude agent markdown frontmatter for Antigravity:
// 1. Replaces incompatible model: lines with model: inherit (or pro/flash).
// 2. Ensures tools: is present with native Antigravity tools (including run_command/bash),
//    since agy restricts agents with no tools line to a minimal read-only set.
func sanitizeAgyAgentContent(b []byte) []byte {
	s := string(b)
	trimmed := strings.TrimLeft(s, "\r\n\t ")
	if !strings.HasPrefix(trimmed, "---") {
		return b
	}
	rest := trimmed[3:]
	idx := strings.Index(rest, "---")
	if idx == -1 {
		return b
	}
	header := rest[:idx]

	// 1. Sanitize model if present
	loc := agentModelLineRe.FindStringSubmatchIndex(header)
	if loc != nil {
		val := strings.TrimSpace(header[loc[2]:loc[3]])
		mapped := "inherit"
		switch val {
		case "inherit", "flash_lite", "flash", "pro":
			mapped = val
		default:
			switch strings.ToLower(val) {
			case "sonnet", "opus", "claude-sonnet-4-6", "claude-opus-4-6-thinking":
				mapped = "pro"
			case "haiku":
				mapped = "flash"
			default:
				mapped = "inherit"
			}
		}
		header = header[:loc[0]] + "model: " + mapped + header[loc[1]:]
	}

	// 2. Sanitize or inject tools so agy agents always have bash (run_command) and write capabilities
	toolsLoc := agentToolsLineRe.FindStringSubmatchIndex(header)
	if toolsLoc != nil {
		rawTools := header[toolsLoc[2]:toolsLoc[3]]
		mappedTools := mapClaudeToolsToAgy(rawTools)
		header = header[:toolsLoc[0]] + "tools: [" + strings.Join(mappedTools, ", ") + "]" + header[toolsLoc[1]:]
	} else {
		// agy defaults custom agents with no tools line to a minimal read-only set.
		// Grant standard developer tools including run_command (bash).
		header = strings.TrimRight(header, "\r\n") + "\ntools: [read_file, edit_file, write_file, run_command, manage_task, search_web]\n"
	}

	prefix := s[:len(s)-len(trimmed)] + "---"
	suffix := rest[idx:]
	return []byte(prefix + header + suffix)
}

// ensureAgyAgentAvailable ensures that an agent defined in ~/.claude/agents/<name>.md
// is also available and compatible in ~/.gemini/config/agents/<name>.md for agy.
func ensureAgyAgentAvailable(name string) {
	h, err := os.UserHomeDir()
	if err != nil {
		return
	}
	geminiAgent := filepath.Join(h, ".gemini", "config", "agents", name+".md")
	claudeAgent := filepath.Join(h, ".claude", "agents", name+".md")

	var b []byte
	if cb, err := os.ReadFile(claudeAgent); err == nil {
		b = cb
	} else if gb, err := os.ReadFile(geminiAgent); err == nil {
		b = gb
	} else {
		return
	}

	sanitized := sanitizeAgyAgentContent(b)
	if existing, err := os.ReadFile(geminiAgent); err == nil && string(existing) == string(sanitized) {
		return
	}
	_ = os.MkdirAll(filepath.Dir(geminiAgent), 0o755)
	_ = os.WriteFile(geminiAgent, sanitized, 0o644)
}

// EnsureAllAgentsSanitized ensures all agents in ~/.gemini/config/agents and ~/.claude/agents
// are configured with valid agy models and native tools (including run_command).
func EnsureAllAgentsSanitized() error {
	h, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	geminiDir := filepath.Join(h, ".gemini", "config", "agents")
	claudeDir := filepath.Join(h, ".claude", "agents")
	_ = os.MkdirAll(geminiDir, 0o755)

	files := make(map[string]bool)
	if gEntries, err := os.ReadDir(geminiDir); err == nil {
		for _, e := range gEntries {
			if strings.HasSuffix(e.Name(), ".md") {
				files[strings.TrimSuffix(e.Name(), ".md")] = true
			}
		}
	}
	if cEntries, err := os.ReadDir(claudeDir); err == nil {
		for _, e := range cEntries {
			if strings.HasSuffix(e.Name(), ".md") {
				files[strings.TrimSuffix(e.Name(), ".md")] = true
			}
		}
	}

	for name := range files {
		ensureAgyAgentAvailable(name)
	}
	return nil
}

// Launch starts `agy --print ...` as a detached OS process, writing its output to
// a log file under conveyorHome, and returns the PID as the bg-id string.
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
	go func() { _ = cmd.Wait() }()
	convID := DetectConversationID(cmd.Process.Pid)
	return pid, convID, nil
}

// DetectConversationID polls for the agy conversation UUID by inspecting the process's open file descriptors.
func DetectConversationID(pid int) string {
	convRe := regexp.MustCompile(`[0-9a-f-]{36}`)
	fdDir := fmt.Sprintf("/proc/%d/fd", pid)
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		entries, err := os.ReadDir(fdDir)
		if err == nil {
			for _, e := range entries {
				target, err := os.Readlink(filepath.Join(fdDir, e.Name()))
				if err != nil {
					continue
				}
				if (strings.Contains(target, "conversations") && strings.HasSuffix(target, ".db")) ||
					(strings.Contains(target, "presence") && strings.HasSuffix(target, ".lock")) {
					if m := convRe.FindString(target); m != "" {
						return m
					}
				}
			}
		}
	}
	return ""
}

// bgLogPath returns the log file path for a bg agy run.
func bgLogPath(home, name string) string {
	return filepath.Join(home, "agy-logs", name+".log")
}

// BgLogPath exports bgLogPath for use by the runner package.
func BgLogPath(home, name string) string {
	return bgLogPath(home, name)
}

var conversationRe = regexp.MustCompile(`(?i)conversation(?:\s+id)?:\s*([0-9a-f-]{36})`)

// ExtractConversationID parses the conversation UUID from agy print output.
func ExtractConversationID(out string) string {
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
		return fmt.Errorf("agy stop signal: %w", err)
	}
	return nil
}

func q(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// AttachCommand returns a command to attach to a background agy session.
// Rather than halting/killing the background session, it streams the session output
// and then drops into an interactive agy conversation with --dangerously-skip-permissions.
func AttachCommand(bgID string) string {
	if pid, err := strconv.Atoi(bgID); err == nil {
		convID := DetectConversationID(pid)
		if IsPIDRunning(pid) {
			logPath := ""
			if target, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/1", pid)); err == nil && target != "" {
				logPath = target
			}
			if logPath != "" {
				if runtime.GOOS == "linux" {
					tailCmd := fmt.Sprintf("tail -n +1 -f --pid=%s %s", bgID, q(logPath))
					if convID != "" {
						return fmt.Sprintf("%s; agy --dangerously-skip-permissions --conversation %s", tailCmd, q(convID))
					}
					return tailCmd
				}
				return fmt.Sprintf("tail -n +1 -f %s", q(logPath))
			}
		}
		if convID != "" {
			return "agy --dangerously-skip-permissions --conversation " + q(convID)
		}
	}
	return "echo 'Conveyor: agy runner (PID " + bgID + ") attach not available'"
}

// ResumeCommand returns the agy command to resume a conversation interactively.
func ResumeCommand(sessionID, settingsPath string, addDirs []string) string {
	c := "agy"
	if settingsPath != "" {
		c += " --settings " + q(settingsPath)
	}
	c += " --dangerously-skip-permissions"
	c += " --conversation " + q(sessionID)
	for _, d := range addDirs {
		c += " --add-dir " + q(d)
	}
	return c
}

// FreshCommand returns the agy command to start a fresh interactive session primed with the brief.
func FreshCommand(settingsPath, briefPath string, addDirs []string) string {
	c := "agy"
	if settingsPath != "" {
		c += " --settings " + q(settingsPath)
	}
	c += " --dangerously-skip-permissions"
	c += " --prompt-interactive " + q("Read "+briefPath+" first, then wait for my instructions.")
	for _, d := range addDirs {
		c += " --add-dir " + q(d)
	}
	return c
}

// WriteSettings writes <home>/agy-settings.json atomically and returns its path.
// It loads base settings from ~/.gemini/antigravity-cli/settings.json if present to inherit user preferences
// and adds Conveyor task configuration.
func WriteSettings(home, conveyorBin string) (string, error) {
	cfg := map[string]any{}
	if basePath, err := userSettingsPath(); err == nil {
		if b, err := os.ReadFile(basePath); err == nil {
			_ = json.Unmarshal(b, &cfg)
		}
	}
	delete(cfg, "ide")

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal agy settings: %w", err)
	}

	path := filepath.Join(home, "agy-settings.json")
	if err := writeAtomic(path, append(b, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write agy settings: %w", err)
	}
	return path, nil
}

// writeAtomic writes b to path via a temp file in the same directory.
func writeAtomic(path string, b []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
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
// --print process with --conversation <sessionID> and --settings <settingsPath>).
func Resume(ctx context.Context, conveyorHome, cwd, bgID, conversationID, settingsPath, prompt string, addDirs []string, s LaunchSpec) (string, error) {
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
	if settingsPath != "" {
		args = append(args, "--settings", settingsPath)
	}
	if s.Bypass {
		args = append(args, "--dangerously-skip-permissions")
	}
	if s.Model != "" {
		args = append(args, "--model", sanitizeAgyModel(s.Model))
	}
	if s.Agent != "" {
		ensureAgyAgentAvailable(s.Agent)
		args = append(args, "--agent", s.Agent)
	}
	for _, d := range addDirs {
		args = append(args, "--add-dir", d)
	}

	cmd := execCommand(ctx, "agy", args...)
	cmd.Dir = cwd
	cmd.Stdout = logFile
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

// EnsureReader is a helper for test mocks.
var _ io.Reader = (*os.File)(nil)
