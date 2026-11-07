package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayaSuryaOolio/conveyor/internal/core"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

func TestTaskOpenPrint(t *testing.T) {
	home := t.TempDir()
	db, err := store.Open(core.DBPath(home))
	if err != nil {
		t.Fatal(err)
	}
	store.AddRepo(db, store.Repo{Name: "r", Path: "/x/r", DefaultBranch: "main"})
	s := &core.Service{DB: db, Home: home}
	tk, err := s.CreateTask(core.NewTaskInput{Title: "x", Repos: []string{"r"}})
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(home, "my ws", "r")
	tk.Repos[0].WorktreePath = &wt
	if err := store.UpdateTaskRepo(db, tk.Repos[0]); err != nil {
		t.Fatal(err)
	}
	db.Close()

	wantTerm := "osascript"
	wantCd := `cd '\''` + wt + `'\'' && :`
	if store.Defaults["terminal_template"] != "" && !strings.Contains(store.Defaults["terminal_template"], "osascript") {
		wantTerm = "x-terminal-emulator"
		wantCd = `cd '` + wt + `' && :`
	}
	if out := cv(t, home, "task", "open", tk.Key, "--print"); !strings.Contains(out, wantTerm) || !strings.Contains(out, wantCd) {
		t.Fatal(out)
	}
	if out := cv(t, home, "task", "open", tk.Key, "--fresh", "--print"); !strings.Contains(out, "claude --settings") || !strings.Contains(out, "BRIEF.md") {
		t.Fatal(out)
	}
	cv(t, home, "app", "add", "Ed", "--command", "ed {cwd}")
	if out := cv(t, home, "app", "list"); !strings.Contains(out, "ed {cwd}") {
		t.Fatal(out)
	}
	if out := cv(t, home, "app", "open", "Ed", tk.Key, "--print"); strings.TrimSpace(out) != "ed '"+wt+"'" {
		t.Fatal(out)
	}
	if out := cv(t, home, "app", "open", "Ed", home, "--print"); strings.TrimSpace(out) != "ed '"+home+"'" {
		t.Fatal(out)
	}
	cv(t, home, "app", "rm", "Ed")
	if out := cv(t, home, "app", "list"); strings.Contains(out, "Ed") {
		t.Fatal(out)
	}
}
