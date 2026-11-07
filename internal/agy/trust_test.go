package agy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAddTrustedWorkspaces_NewFile(t *testing.T) {
	tmp := t.TempDir()
	settingsFile := filepath.Join(tmp, "gemini", "antigravity-cli", "settings.json")

	oldPath := userSettingsPath
	defer func() { userSettingsPath = oldPath }()
	userSettingsPath = func() (string, error) { return settingsFile, nil }

	repoRoot := filepath.Join(tmp, "my-repo")
	conveyorHome := filepath.Join(tmp, ".conveyor")

	if err := AddTrustedWorkspaces(conveyorHome, repoRoot); err != nil {
		t.Fatalf("AddTrustedWorkspaces: %v", err)
	}

	list, err := GetTrustedWorkspaces()
	if err != nil {
		t.Fatalf("GetTrustedWorkspaces: %v", err)
	}

	foundRepo := false
	foundConvHomeWorkspaces := false
	for _, p := range list {
		if p == repoRoot {
			foundRepo = true
		}
		if p == filepath.Join(conveyorHome, "workspaces") {
			foundConvHomeWorkspaces = true
		}
	}
	if !foundRepo {
		t.Errorf("repo root %q not found in trustedWorkspaces: %v", repoRoot, list)
	}
	if !foundConvHomeWorkspaces {
		t.Errorf("conveyorHome workspaces %q not found in trustedWorkspaces: %v", filepath.Join(conveyorHome, "workspaces"), list)
	}
}

func TestAddTrustedWorkspaces_PreserveExisting(t *testing.T) {
	tmp := t.TempDir()
	settingsDir := filepath.Join(tmp, "gemini", "antigravity-cli")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsFile := filepath.Join(settingsDir, "settings.json")

	initial := map[string]any{
		"model":       "gemini-2.5",
		"colorScheme": "dark",
		"trustedWorkspaces": []string{
			"/existing/workspace",
		},
	}
	b, _ := json.Marshal(initial)
	if err := os.WriteFile(settingsFile, b, 0o644); err != nil {
		t.Fatal(err)
	}

	oldPath := userSettingsPath
	defer func() { userSettingsPath = oldPath }()
	userSettingsPath = func() (string, error) { return settingsFile, nil }

	repoRoot := filepath.Join(tmp, "repo-2")
	if err := AddTrustedWorkspaces("", repoRoot); err != nil {
		t.Fatalf("AddTrustedWorkspaces: %v", err)
	}

	content, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	var read map[string]any
	if err := json.Unmarshal(content, &read); err != nil {
		t.Fatal(err)
	}
	if read["model"] != "gemini-2.5" {
		t.Errorf("expected model=gemini-2.5, got %v", read["model"])
	}
	if read["colorScheme"] != "dark" {
		t.Errorf("expected colorScheme=dark, got %v", read["colorScheme"])
	}

	list, err := GetTrustedWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	foundExisting := false
	foundRepo := false
	for _, p := range list {
		if p == "/existing/workspace" {
			foundExisting = true
		}
		if p == repoRoot {
			foundRepo = true
		}
	}
	if !foundExisting {
		t.Errorf("expected /existing/workspace preserved, got %v", list)
	}
	if !foundRepo {
		t.Errorf("expected %q added, got %v", repoRoot, list)
	}

	// Idempotency check: call again
	if err := AddTrustedWorkspaces("", repoRoot); err != nil {
		t.Fatalf("AddTrustedWorkspaces second call: %v", err)
	}
	list2, _ := GetTrustedWorkspaces()
	countRepo := 0
	for _, p := range list2 {
		if p == repoRoot {
			countRepo++
		}
	}
	if countRepo != 1 {
		t.Errorf("expected repoRoot to appear exactly once, appeared %d times", countRepo)
	}
}
