package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/JayaSuryaOolio/conveyor/internal/agy"
)

// AgyRunner wraps the agy package as a Runner.
// Because agy does not have a built-in session-list command, Agents() returns
// an empty list; the dispatch reap logic uses IsPIDRunning instead.
type AgyRunner struct {
	// ConveyorHome is the conveyor home directory (used for log files).
	ConveyorHome string
}

// NewAgyRunner returns an Antigravity (agy) runner.
func NewAgyRunner(home string) Runner { return &AgyRunner{ConveyorHome: home} }

func (a *AgyRunner) Name() string { return "agy" }

func (a *AgyRunner) Launch(ctx context.Context, s LaunchSpec) (string, error) {
	bgID, _, err := agy.Launch(ctx, a.ConveyorHome, agy.LaunchSpec{
		Cwd:     s.Cwd,
		AddDirs: s.AddDirs,
		Prompt:  s.Prompt,
		Name:    s.TaskKey,
		Model:   s.Model,
		Agent:   s.Agent,
		Bypass:  s.Bypass,
	})
	return bgID, err
}

func (a *AgyRunner) Resume(ctx context.Context, cwd, bgID, sessionID, prompt string) (string, error) {
	// sessionID is the agy conversation UUID for resume.
	return agy.Resume(ctx, a.ConveyorHome, cwd, bgID, sessionID, prompt, agy.LaunchSpec{
		Cwd:  cwd,
		Name: taskKeyFromBgID(bgID),
	})
}

// taskKeyFromBgID extracts the task key from a bg log path — best-effort fallback.
func taskKeyFromBgID(bgID string) string {
	// bgID is just a PID string for agy; we don't have the task key here.
	// The log file names after the task key, so this is not extractable from bgID.
	return "task-" + bgID
}

func (a *AgyRunner) Stop(ctx context.Context, bgID string) error {
	return agy.Stop(ctx, bgID)
}

func (a *AgyRunner) Remove(_ context.Context, _ string) error { return nil }

func (a *AgyRunner) Agents(_ context.Context) ([]Agent, error) {
	// agy has no session-list command; return empty. The dispatcher uses
	// IsPIDRunning for agy reaping (see dispatch/dispatch.go).
	return nil, nil
}

func (a *AgyRunner) AttachCommand(bgID string) string {
	return agy.AttachCommand(bgID)
}

func (a *AgyRunner) ResumeCommand(sessionID, _ string) string {
	// settingsPath is not used by agy.
	return agy.ResumeCommand(sessionID)
}

func (a *AgyRunner) FreshCommand(_, briefPath string, addDirs []string) string {
	// settingsPath (first arg) is not used by agy.
	return agy.FreshCommand(briefPath, addDirs)
}

func (a *AgyRunner) WriteSettings(home, conveyorBin string) (string, error) {
	return agy.WriteSettings(home, conveyorBin)
}

// IsPIDRunning reports whether an agy bg-id (a PID string) is still running.
func IsPIDRunning(bgID string) bool {
	pid, err := strconv.Atoi(bgID)
	if err != nil {
		return false
	}
	return agy.IsPIDRunning(pid)
}

// BgLogPath returns the path to the agy background session log for a task.
func BgLogPath(home, taskKey string) string {
	return filepath.Join(home, "agy-logs", taskKey+".log")
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
