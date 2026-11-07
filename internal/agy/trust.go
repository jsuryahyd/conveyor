package agy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var trustMu sync.Mutex

// userSettingsPath resolves the path to ~/.gemini/antigravity-cli/settings.json.
// Overridable via CONVEYOR_AGY_SETTINGS or in tests.
var userSettingsPath = func() (string, error) {
	if p := os.Getenv("CONVEYOR_AGY_SETTINGS"); p != "" {
		return p, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}
	return filepath.Join(h, ".gemini", "antigravity-cli", "settings.json"), nil
}

// UserSettingsPath returns the current path to the agy settings.json file.
func UserSettingsPath() (string, error) {
	return userSettingsPath()
}

// AddTrustedWorkspaces updates ~/.gemini/antigravity-cli/settings.json to add
// the given repo/workspace paths, Conveyor workspaces (~/.conveyor/workspaces and
// conveyorHome/workspaces), and ~/.config/gh to trustedWorkspaces.
// Existing settings and formatting are preserved; already trusted paths are not duplicated.
func AddTrustedWorkspaces(conveyorHome string, paths ...string) error {
	trustMu.Lock()
	defer trustMu.Unlock()

	settingsPath, err := userSettingsPath()
	if err != nil {
		return err
	}

	// Collect all paths to trust
	var toTrust []string

	userHome, _ := os.UserHomeDir()
	if userHome != "" {
		toTrust = append(toTrust, filepath.Join(userHome, ".conveyor", "workspaces"))
		toTrust = append(toTrust, filepath.Join(userHome, ".config", "gh"))
	}
	if conveyorHome != "" {
		toTrust = append(toTrust, filepath.Join(conveyorHome, "workspaces"))
	}
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		toTrust = append(toTrust, p)
		if resolved, err := filepath.EvalSymlinks(p); err == nil && resolved != p {
			toTrust = append(toTrust, resolved)
		}
	}

	// Normalize candidates
	var candidates []string
	seenCand := map[string]bool{}
	for _, p := range toTrust {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		p = filepath.Clean(p)
		if !seenCand[p] {
			seenCand[p] = true
			candidates = append(candidates, p)
		}
	}

	// Read existing settings
	cfg := map[string]any{}
	if b, err := os.ReadFile(settingsPath); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}

	// Extract existing trustedWorkspaces
	var currentList []string
	existingNorm := map[string]bool{}
	if raw, ok := cfg["trustedWorkspaces"]; ok {
		if arr, ok := raw.([]any); ok {
			for _, item := range arr {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					cleaned := filepath.Clean(s)
					currentList = append(currentList, s)
					existingNorm[cleaned] = true
				}
			}
		} else if arr, ok := raw.([]string); ok {
			for _, s := range arr {
				if strings.TrimSpace(s) != "" {
					cleaned := filepath.Clean(s)
					currentList = append(currentList, s)
					existingNorm[cleaned] = true
				}
			}
		}
	}

	changed := false
	for _, cand := range candidates {
		if !existingNorm[cand] {
			currentList = append(currentList, cand)
			existingNorm[cand] = true
			changed = true
		}
	}

	// If nothing changed and file already exists, nothing to write
	if !changed {
		if _, err := os.Stat(settingsPath); err == nil {
			return nil
		}
	}

	cfg["trustedWorkspaces"] = currentList

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agy settings: %w", err)
	}
	b = append(b, '\n')

	return writeAtomic(settingsPath, b, 0o644)
}

// GetTrustedWorkspaces reads ~/.gemini/antigravity-cli/settings.json and returns
// the list of trustedWorkspaces.
func GetTrustedWorkspaces() ([]string, error) {
	trustMu.Lock()
	defer trustMu.Unlock()

	settingsPath, err := userSettingsPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	var cfg struct {
		TrustedWorkspaces []string `json:"trustedWorkspaces"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	if cfg.TrustedWorkspaces == nil {
		return []string{}, nil
	}
	return cfg.TrustedWorkspaces, nil
}
