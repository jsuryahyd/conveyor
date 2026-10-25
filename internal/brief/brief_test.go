package brief

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestRenderGolden(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r, _ := store.AddRepo(db, store.Repo{Name: "api", Path: "/src/api", DefaultBranch: "main"})
	tk, err := store.CreateTask(db, store.Task{Title: "Add login", Description: "Users can log in.", Status: store.StatusTodo},
		[]store.TaskRepo{{RepoID: r.ID, IsPrimary: true, Branch: "login"}})
	if err != nil {
		t.Fatal(err)
	}
	wt, pr := "/ws/CV-1/api", "https://example.com/pr/1"
	tk.Repos[0].WorktreePath, tk.Repos[0].PRURL = &wt, &pr
	store.UpdateTaskRepo(db, tk.Repos[0])
	for i, s := range []string{"first", "second\nline two", "third", "fourth"} {
		run, _ := store.CreateRun(db, store.Run{TaskID: tk.ID, Kind: "auto", Mode: "fresh", Status: "launching"})
		run.Summary = &s
		if i == 3 {
			st, nx, cr := "branch cv/CV-1-login, tests: pass", "wire up UI", "- [x] AC1 - done\n- [ ] AC2 - todo"
			run.State, run.Next, run.Criteria = &st, &nx, &cr
		}
		store.UpdateRun(db, run)
	}
	for _, c := range []store.Comment{{Author: store.AuthorHuman, Body: "please add tests\nmore detail"}, {Author: store.AuthorAgent, Body: "ignored"}, {Author: store.AuthorHuman, Body: "thanks"}} {
		c.TaskID = tk.ID
		store.AddComment(db, c)
	}
	tk, _ = store.GetTask(db, tk.ID)
	path, err := Render(db, dir, tk)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "workspaces", "CV-1", "BRIEF.md") {
		t.Fatalf("path %s", path)
	}
	got, _ := os.ReadFile(path)
	golden := filepath.Join("testdata", "brief.golden")
	if *update {
		os.MkdirAll("testdata", 0o755)
		os.WriteFile(golden, got, 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("brief differs from %s (run with -update):\n%s", golden, got)
	}
}

func TestPrompts(t *testing.T) {
	tk := store.Task{Key: "CV-2", Title: "T"}
	d := []store.Comment{{Author: "human", Body: "a"}, {Author: "system", Body: "b"}}
	if got, want := FreshPrompt(tk, "/b.md", nil, ""), "You are working on Conveyor task CV-2: T. Read /b.md first and follow it."; got != want {
		t.Errorf("fresh %q", got)
	}
	if got := FreshPrompt(tk, "/b.md", d, ""); !strings.HasSuffix(got, "\n- [human] a\n- [system] b") {
		t.Errorf("fresh delta %q", got)
	}
	if got, want := ResumePrompt(tk, "/b.md", d, ""), "New comments on CV-2:\n- [human] a\n- [system] b\n(The brief at /b.md is up to date.)"; got != want {
		t.Errorf("resume %q", got)
	}
}

func TestSettingsShapeBrief(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tk := store.Task{Key: "CV-3", Title: "T"}
	store.SetSetting(db, "brief_extra_rules", "- Be kind.")
	store.SetSetting(db, "pr_comment_prefix", "bot: ")
	got, err := Build(db, tk)
	if err != nil || !strings.Contains(got, "with `bot: `.\n- Be kind.\n\n## Older history") {
		t.Fatalf("rules not applied (%v):\n%s", err, got)
	}
	if _, err := os.Stat(filepath.Join(dir, "workspaces")); err == nil {
		t.Fatal("Build must not write")
	}
	store.SetSetting(db, "pr_comment_prefix", "")
	if got, _ = Build(db, tk); strings.Contains(got, "Start every comment") {
		t.Fatal("empty prefix should drop the rule")
	}
	d := []store.Comment{{Author: "human", Body: "a"}}
	if got := FreshPrompt(tk, "/b.md", d, "{key}|{title}|{brief}|{comments}"); got != "CV-3|T|/b.md|- [human] a" {
		t.Errorf("custom fresh %q", got)
	}
	if got := ResumePrompt(tk, "/b.md", d, "{key}!"); got != "CV-3!" {
		t.Errorf("custom resume %q", got)
	}
}

func TestAgyRulesAndContext(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runnerName := "agy"
	tk := store.Task{Key: "CV-4", Title: "Agy Task", Runner: &runnerName}
	got, err := Build(db, tk)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "The repo's AGENTS.md, skills, commands and agents are the authority") {
		t.Errorf("brief should mention AGENTS.md for agy runner:\n%s", got)
	}
}

