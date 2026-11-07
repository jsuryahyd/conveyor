package runner

import (
	"context"
	"path/filepath"
	"strconv"

	"github.com/JayaSuryaOolio/conveyor/internal/agy"
)

// AgyRunner wraps the agy package as a Runner.
type AgyRunner struct {
	// ConveyorHome is the conveyor home directory (used for log files).
	ConveyorHome string
}

// NewAgyRunner returns an Antigravity (agy) runner.
func NewAgyRunner(home string) Runner { return &AgyRunner{ConveyorHome: home} }

func (a *AgyRunner) Name() string { return "agy" }

func (a *AgyRunner) Launch(ctx context.Context, s LaunchSpec) (string, string, error) {
	_ = agy.AddTrustedWorkspaces(a.ConveyorHome)
	bgID, convID, err := agy.Launch(ctx, a.ConveyorHome, agy.LaunchSpec{
		Cwd:          s.Cwd,
		AddDirs:      s.AddDirs,
		Prompt:       s.Prompt,
		Name:         s.TaskKey,
		SettingsPath: s.SettingsPath,
		Model:        s.Model,
		Agent:        s.Agent,
		Bypass:       s.Bypass,
	})
	return bgID, convID, err
}

func (a *AgyRunner) Resume(ctx context.Context, cwd, bgID, sessionID, settingsPath, prompt string, addDirs []string) (string, error) {
	return agy.Resume(ctx, a.ConveyorHome, cwd, bgID, sessionID, settingsPath, prompt, addDirs, agy.LaunchSpec{
		Cwd:          cwd,
		Name:         taskKeyFromBgID(bgID),
		SettingsPath: settingsPath,
		Bypass:       true,
	})
}

// taskKeyFromBgID extracts the task key from a bg log path — best-effort fallback.
func taskKeyFromBgID(bgID string) string {
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

func (a *AgyRunner) ResumeCommand(sessionID, settingsPath string, addDirs []string) string {
	return agy.ResumeCommand(sessionID, settingsPath, addDirs)
}

func (a *AgyRunner) FreshCommand(settingsPath, briefPath string, addDirs []string) string {
	return agy.FreshCommand(settingsPath, briefPath, addDirs)
}

func (a *AgyRunner) WriteSettings(home, conveyorBin string) (string, error) {
	_ = agy.AddTrustedWorkspaces(home)
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

// DetectConversationID polls for the agy conversation UUID by inspecting the process's open file descriptors.
func DetectConversationID(pid int) string {
	return agy.DetectConversationID(pid)
}

// BgLogPath returns the path to the agy background session log for a task.
func BgLogPath(home, taskKey string) string {
	return filepath.Join(home, "agy-logs", taskKey+".log")
}
