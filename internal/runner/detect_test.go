package runner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

func TestDetectRunners(t *testing.T) {
	origLookPath := lookPath
	defer func() { lookPath = origLookPath }()

	lookPath = func(file string) (string, error) {
		if file == "agy" {
			return "/usr/local/bin/agy", nil
		}
		return "", errors.New("not found")
	}

	agents := DetectRunners()
	if len(agents) < 2 {
		t.Fatalf("expected at least 2 runners, got %d", len(agents))
	}
	var agyFound, claudeFound bool
	for _, a := range agents {
		if a.ID == "agy" {
			agyFound = true
			if !a.Installed || a.Path != "/usr/local/bin/agy" {
				t.Errorf("agy agent not correctly detected: %+v", a)
			}
		}
		if a.ID == "claude" {
			claudeFound = true
			if a.Installed {
				t.Errorf("claude should not be detected: %+v", a)
			}
		}
	}
	if !agyFound || !claudeFound {
		t.Errorf("missing runners in detection list")
	}
}

func TestAutoConfigureDefaultRunner(t *testing.T) {
	origLookPath := lookPath
	defer func() { lookPath = origLookPath }()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Case 1: claude missing, agy present -> auto configure to agy
	lookPath = func(file string) (string, error) {
		if file == "agy" {
			return "/usr/bin/agy", nil
		}
		return "", os.ErrNotExist
	}

	runner, changed, err := AutoConfigureDefaultRunner(db)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || runner != "agy" {
		t.Fatalf("expected changed=true runner=agy, got changed=%v runner=%s", changed, runner)
	}

	// Calling again when already agy should report changed=false
	runner, changed, err = AutoConfigureDefaultRunner(db)
	if err != nil {
		t.Fatal(err)
	}
	if changed || runner != "agy" {
		t.Fatalf("expected changed=false runner=agy, got changed=%v runner=%s", changed, runner)
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
