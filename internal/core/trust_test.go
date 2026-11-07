package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayaSuryaOolio/conveyor/internal/agy"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
}

func TestAddRepo_TrustsWorkspaces(t *testing.T) {
	tmp := t.TempDir()
	settingsFile := filepath.Join(tmp, "gemini-settings.json")
	t.Setenv("CONVEYOR_AGY_SETTINGS", settingsFile)

	db, err := store.Open(filepath.Join(tmp, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	repoDir := filepath.Join(tmp, "my-repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repoDir)

	svc := &Service{DB: db, Home: filepath.Join(tmp, ".conveyor")}
	repo, err := svc.AddRepo(repoDir, "my-repo")
	if err != nil {
		t.Fatalf("AddRepo: %v", err)
	}

	trusted, err := agy.GetTrustedWorkspaces()
	if err != nil {
		t.Fatalf("GetTrustedWorkspaces: %v", err)
	}

	hasRepo := false
	hasWorkspaces := false
	hasGH := false
	for _, p := range trusted {
		if p == repo.Path {
			hasRepo = true
		}
		if p == filepath.Join(svc.Home, "workspaces") {
			hasWorkspaces = true
		}
		if strings.HasSuffix(p, filepath.Join(".config", "gh")) {
			hasGH = true
		}
	}
	if !hasRepo {
		t.Errorf("repo path %s not found in trusted: %v", repo.Path, trusted)
	}
	if !hasWorkspaces {
		t.Errorf("workspaces dir not found in trusted: %v", trusted)
	}
	if !hasGH {
		t.Errorf(".config/gh not found in trusted: %v", trusted)
	}
}

func TestSetSetting_DefaultRunnerAgy_TrustsWorkspaces(t *testing.T) {
	tmp := t.TempDir()
	settingsFile := filepath.Join(tmp, "gemini-settings.json")
	t.Setenv("CONVEYOR_AGY_SETTINGS", settingsFile)

	db, err := store.Open(filepath.Join(tmp, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	svc := &Service{DB: db, Home: filepath.Join(tmp, ".conveyor")}
	store.AddRepo(db, store.Repo{Name: "r1", Path: filepath.Join(tmp, "r1"), DefaultBranch: "main"})

	if err := svc.SetSetting("default_runner", "agy"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	trusted, err := agy.GetTrustedWorkspaces()
	if err != nil {
		t.Fatalf("GetTrustedWorkspaces: %v", err)
	}

	hasR1 := false
	hasWorkspaces := false
	for _, p := range trusted {
		if p == filepath.Join(tmp, "r1") {
			hasR1 = true
		}
		if p == filepath.Join(svc.Home, "workspaces") {
			hasWorkspaces = true
		}
	}
	if !hasR1 {
		t.Errorf("r1 path not found in trusted: %v", trusted)
	}
	if !hasWorkspaces {
		t.Errorf("workspaces not found in trusted: %v", trusted)
	}
}

func TestSetTaskRunner_Agy_TrustsWorkspaces(t *testing.T) {
	tmp := t.TempDir()
	settingsFile := filepath.Join(tmp, "gemini-settings.json")
	t.Setenv("CONVEYOR_AGY_SETTINGS", settingsFile)

	db, err := store.Open(filepath.Join(tmp, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	svc := &Service{DB: db, Home: filepath.Join(tmp, ".conveyor")}
	store.AddRepo(db, store.Repo{Name: "task-repo", Path: filepath.Join(tmp, "task-repo"), DefaultBranch: "main"})

	task, err := svc.CreateTask(NewTaskInput{
		Title: "Test Task",
		Repos: []string{"task-repo"},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := svc.SetTaskRunner(task.Key, "agy"); err != nil {
		t.Fatalf("SetTaskRunner: %v", err)
	}

	trusted, err := agy.GetTrustedWorkspaces()
	if err != nil {
		t.Fatalf("GetTrustedWorkspaces: %v", err)
	}

	hasTaskRepo := false
	for _, p := range trusted {
		if p == filepath.Join(tmp, "task-repo") {
			hasTaskRepo = true
		}
	}
	if !hasTaskRepo {
		t.Errorf("task-repo not found in trusted: %v", trusted)
	}
}

func TestCreateTask_RunnerAgy_TrustsWorkspaces(t *testing.T) {
	tmp := t.TempDir()
	settingsFile := filepath.Join(tmp, "gemini-settings.json")
	t.Setenv("CONVEYOR_AGY_SETTINGS", settingsFile)

	db, err := store.Open(filepath.Join(tmp, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	svc := &Service{DB: db, Home: filepath.Join(tmp, ".conveyor")}
	store.AddRepo(db, store.Repo{Name: "created-repo", Path: filepath.Join(tmp, "created-repo"), DefaultBranch: "main"})

	_, err = svc.CreateTask(NewTaskInput{
		Title:  "Task with agy",
		Repos:  []string{"created-repo"},
		Runner: "agy",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	trusted, err := agy.GetTrustedWorkspaces()
	if err != nil {
		t.Fatalf("GetTrustedWorkspaces: %v", err)
	}

	hasCreatedRepo := false
	for _, p := range trusted {
		if p == filepath.Join(tmp, "created-repo") {
			hasCreatedRepo = true
		}
	}
	if !hasCreatedRepo {
		t.Errorf("created-repo not found in trusted: %v", trusted)
	}
}
