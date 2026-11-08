package runner

import (
	"path/filepath"
	"testing"
)

func TestForTask(t *testing.T) {
	home := "/test/home"
	if r := ForTask("", "claude", home); r.Name() != "claude" {
		t.Errorf("expected claude, got %s", r.Name())
	}
	if r := ForTask("", "agy", home); r.Name() != "agy" {
		t.Errorf("expected agy, got %s", r.Name())
	}
	if r := ForTask("agy", "claude", home); r.Name() != "agy" {
		t.Errorf("expected task override agy, got %s", r.Name())
	}
	if r := ForTask("claude", "agy", home); r.Name() != "claude" {
		t.Errorf("expected task override claude, got %s", r.Name())
	}
	if r := ForTask("", "", home); r.Name() != "claude" {
		t.Errorf("expected fallback claude, got %s", r.Name())
	}
}

func TestValidateRunner(t *testing.T) {
	for _, valid := range []string{"", "claude", "agy"} {
		if err := ValidateRunner(valid); err != nil {
			t.Errorf("expected %q valid, got %v", valid, err)
		}
	}
	for _, invalid := range []string{"openai", "unknown"} {
		if err := ValidateRunner(invalid); err == nil {
			t.Errorf("expected %q invalid", invalid)
		}
	}
}

func TestBgLogPath(t *testing.T) {
	got := BgLogPath("/home", "CV-1")
	want := filepath.Join("/home", "agy-logs", "CV-1.log")
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestResumeSpecTaskKeyLogPath(t *testing.T) {
	spec := ResumeSpec{
		TaskKey: "CV-10",
		BgID:    "12345",
	}
	name := spec.TaskKey
	if name == "" {
		name = taskKeyFromBgID(spec.BgID)
	}
	if name != "CV-10" {
		t.Errorf("got %q, want CV-10", name)
	}
	if BgLogPath("/conveyor", name) != "/conveyor/agy-logs/CV-10.log" {
		t.Errorf("expected aligned log path for CV-10")
	}
}
