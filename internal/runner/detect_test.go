package runner

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/JayaSuryaOolio/conveyor/internal/agy"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

func TestDetectRunners(t *testing.T) {
	oldLookPath := lookPath
	defer func() { lookPath = oldLookPath }()

	lookPath = func(file string) (string, error) {
		if file == "claude" {
			return "/usr/local/bin/claude", nil
		}
		if file == "agy" {
			return "/usr/bin/agy", nil
		}
		return "", errors.New("not found")
	}

	runners := DetectRunners()
	if len(runners) != 2 {
		t.Fatalf("expected 2 runners, got %d", len(runners))
	}
	for _, r := range runners {
		if !r.Installed {
			t.Errorf("expected %s installed", r.ID)
		}
		if r.Path == "" {
			t.Errorf("expected %s path non-empty", r.ID)
		}
	}
}

func TestAutoConfigureDefaultRunner(t *testing.T) {
	oldLookPath := lookPath
	defer func() { lookPath = oldLookPath }()

	dir := t.TempDir()
	settingsFile := filepath.Join(dir, "agy-settings.json")
	t.Setenv("CONVEYOR_AGY_SETTINGS", settingsFile)
	db, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Case 1: only agy is installed. Current default_runner is claude (default).
	lookPath = func(file string) (string, error) {
		if file == "agy" {
			return "/usr/bin/agy", nil
		}
		return "", errors.New("not found")
	}

	picked, changed, err := AutoConfigureDefaultRunner(db)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || picked != "agy" {
		t.Fatalf("expected switched to agy, got picked=%s changed=%v", picked, changed)
	}
	if got := store.GetSetting(db, "default_runner"); got != "agy" {
		t.Fatalf("expected stored setting agy, got %s", got)
	}

	trusted, err := agy.GetTrustedWorkspaces()
	if err != nil {
		t.Fatalf("GetTrustedWorkspaces: %v", err)
	}
	if len(trusted) == 0 {
		t.Fatalf("expected trusted workspaces to be populated")
	}

	// Case 2: second run with agy installed keeps agy without change.
	picked, changed, err = AutoConfigureDefaultRunner(db)
	if err != nil {
		t.Fatal(err)
	}
	if changed || picked != "agy" {
		t.Fatalf("expected keep agy, got picked=%s changed=%v", picked, changed)
	}

	// Case 3: claude becomes available, but agy is still current and valid.
	lookPath = func(file string) (string, error) {
		return "/bin/" + file, nil
	}
	picked, changed, err = AutoConfigureDefaultRunner(db)
	if err != nil {
		t.Fatal(err)
	}
	if changed || picked != "agy" {
		t.Fatalf("expected keep valid agy even if claude installed, got picked=%s changed=%v", picked, changed)
	}
}

func TestInstructionFileForRunner(t *testing.T) {
	if got := InstructionFileForRunner("agy"); got != "AGENTS.md" {
		t.Errorf("expected AGENTS.md, got %s", got)
	}
	if got := InstructionFileForRunner("claude"); got != "CLAUDE.md" {
		t.Errorf("expected CLAUDE.md, got %s", got)
	}
	if got := InstructionFileForRunner(""); got != "CLAUDE.md" {
		t.Errorf("expected CLAUDE.md, got %s", got)
	}
}
