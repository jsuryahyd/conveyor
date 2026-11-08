// Package runner defines the Runner interface that abstracts agent backends
// (Claude Code and Antigravity/agy). Both backends implement the same lifecycle:
// Launch (fresh background session), Resume (continue an earlier session), Stop,
// Remove, Agents (list live sessions), and terminal command helpers.
package runner

import (
	"context"
	"fmt"
)

// Agent is a live background session reported by the runner's process list.
type Agent struct {
	ID        string // background / session handle used to Stop/Remove
	Name      string // task key
	Cwd       string
	State     string
	Status    string
	SessionID string // transcript ID used for resume
	PID       int
}

// Runner abstracts the agent CLI backend (Claude Code or Antigravity).
type Runner interface {
	// Launch starts a fresh background session and returns the bg id and session id (if immediately known).
	Launch(ctx context.Context, s LaunchSpec) (bgID string, sessionID string, err error)
	// Resume continues session sessionID (stopping bgID first) and returns the new bg id.
	Resume(ctx context.Context, s ResumeSpec) (string, error)
	// Stop signals the background session to stop.
	Stop(ctx context.Context, bgID string) error
	// Remove removes / cleans up the background session record.
	Remove(ctx context.Context, bgID string) error
	// Agents returns all live background sessions (used for reaping).
	Agents(ctx context.Context) ([]Agent, error)
	// AttachCommand returns the shell command to attach to a running bg session.
	AttachCommand(bgID string) string
	// ResumeCommand returns the shell command to resume a finished session interactively.
	ResumeCommand(sessionID, settingsPath string, addDirs []string) string
	// FreshCommand returns the shell command to start a fresh interactive session primed with the brief.
	FreshCommand(settingsPath, briefPath string, addDirs []string) string
	// WriteSettings writes the backend's settings/hook-config file under home and returns its path.
	// conveyorBin is the path to the conveyor CLI that hook commands call back.
	WriteSettings(home, conveyorBin string) (string, error)
	// Name returns the short name of the backend ("claude" or "agy").
	Name() string
}

// LaunchSpec describes a fresh background launch, backend-agnostic.
type LaunchSpec struct {
	Cwd          string
	AddDirs      []string
	Prompt       string
	TaskKey      string // used as the session name
	SettingsPath string
	Model        string
	Agent        string
	MCPConfig    string
	Bypass       bool // skip permission prompts (auto runs)
}

// ResumeSpec describes resuming an existing session, backend-agnostic.
type ResumeSpec struct {
	TaskKey      string
	Cwd          string
	BgID         string
	SessionID    string
	SettingsPath string
	Prompt       string
	AddDirs      []string
	Model        string
	Agent        string
}

// ForTask returns the appropriate Runner for a task, given the task's runner
// field and the default runner from settings. home is the conveyor home dir.
func ForTask(taskRunner, defaultRunner, home string) Runner {
	r := taskRunner
	if r == "" {
		r = defaultRunner
	}
	switch r {
	case "agy":
		return NewAgyRunner(home)
	default:
		return NewClaudeRunner()
	}
}

// ValidateRunner checks that runner is a known backend name or empty.
func ValidateRunner(r string) error {
	switch r {
	case "", "claude", "agy":
		return nil
	}
	return fmt.Errorf("unknown runner %q: must be \"claude\" or \"agy\"", r)
}
