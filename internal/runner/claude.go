package runner

import (
	"context"

	"github.com/JayaSuryaOolio/conveyor/internal/claude"
)

// ClaudeRunner wraps the claude package as a Runner.
type ClaudeRunner struct{}

// NewClaudeRunner returns the Claude Code runner.
func NewClaudeRunner() Runner { return &ClaudeRunner{} }

func (c *ClaudeRunner) Name() string { return "claude" }

func (c *ClaudeRunner) Launch(ctx context.Context, s LaunchSpec) (string, string, error) {
	bgID, err := claude.Launch(ctx, claude.LaunchSpec{
		Cwd:          s.Cwd,
		AddDirs:      s.AddDirs,
		Prompt:       s.Prompt,
		Name:         s.TaskKey,
		SettingsPath: s.SettingsPath,
		Model:        s.Model,
		Agent:        s.Agent,
		MCPConfig:    s.MCPConfig,
		Bypass:       s.Bypass,
	})
	return bgID, "", err
}

func (c *ClaudeRunner) Resume(ctx context.Context, s ResumeSpec) (string, error) {
	return claude.Resume(ctx, s.Cwd, s.BgID, s.SessionID, s.SettingsPath, s.Prompt, s.AddDirs)
}

func (c *ClaudeRunner) Stop(ctx context.Context, bgID string) error {
	return claude.Stop(ctx, bgID)
}

func (c *ClaudeRunner) Remove(ctx context.Context, bgID string) error {
	return claude.Remove(ctx, bgID)
}

func (c *ClaudeRunner) Agents(ctx context.Context) ([]Agent, error) {
	cas, err := claude.Agents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Agent, len(cas))
	for i, a := range cas {
		out[i] = Agent{ID: a.ID, Name: a.Name, Cwd: a.Cwd, State: a.State, Status: a.Status, SessionID: a.SessionID, PID: a.PID}
	}
	return out, nil
}

func (c *ClaudeRunner) AttachCommand(bgID string) string {
	return claude.AttachCommand(bgID)
}

func (c *ClaudeRunner) ResumeCommand(sessionID, settingsPath string, addDirs []string) string {
	return claude.ResumeCommand(sessionID, settingsPath, addDirs)
}

func (c *ClaudeRunner) FreshCommand(settingsPath, briefPath string, addDirs []string) string {
	return claude.FreshCommand(settingsPath, briefPath, addDirs)
}

func (c *ClaudeRunner) WriteSettings(home, conveyorBin string) (string, error) {
	return claude.WriteSettings(home, conveyorBin)
}
