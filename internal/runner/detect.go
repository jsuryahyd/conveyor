package runner

import (
	"database/sql"
	"os/exec"

	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

// CLIAgent represents a detected CLI agent/runner on the system.
type CLIAgent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Command   string `json:"command"`
	Installed bool   `json:"installed"`
	Path      string `json:"path"`
}

// KnownRunners returns the list of all supported runner backends.
func KnownRunners() []CLIAgent {
	return []CLIAgent{
		{ID: "claude", Name: "Claude Code", Command: "claude"},
		{ID: "agy", Name: "Antigravity", Command: "agy"},
	}
}

// lookPath is swappable in tests.
var lookPath = exec.LookPath

// DetectRunners checks PATH for each known runner CLI and returns their status.
func DetectRunners() []CLIAgent {
	agents := KnownRunners()
	for i := range agents {
		if p, err := lookPath(agents[i].Command); err == nil {
			agents[i].Installed = true
			agents[i].Path = p
		}
	}
	return agents
}

// AutoConfigureDefaultRunner inspects the system for installed runner CLIs.
// If default_runner is unset, or if it points to a runner that is not installed
// while another detected runner is installed, it updates default_runner in settings.
// Returns the active runner, whether a change was made, and any error.
func AutoConfigureDefaultRunner(db *sql.DB) (string, bool, error) {
	agents := DetectRunners()
	installed := map[string]string{}
	for _, a := range agents {
		if a.Installed {
			installed[a.ID] = a.Path
		}
	}

	cur := store.GetSetting(db, "default_runner")

	// If the current runner is installed, keep it.
	if _, ok := installed[cur]; ok {
		return cur, false, nil
	}

	// Current runner is NOT installed or empty. If we detected an alternative runner, switch to it.
	// Prefer agy if installed, else claude if installed.
	var picked string
	for _, id := range []string{"agy", "claude"} {
		if _, ok := installed[id]; ok {
			picked = id
			break
		}
	}
	if picked == "" {
		// None installed; leave as is
		return cur, false, nil
	}

	if err := store.SetSetting(db, "default_runner", picked); err != nil {
		return cur, false, err
	}
	return picked, true, nil
}

// InstructionFileForRunner returns the primary context / instructions file for a runner.
func InstructionFileForRunner(r string) string {
	switch r {
	case "agy":
		return "AGENTS.md"
	default:
		return "CLAUDE.md"
	}
}
