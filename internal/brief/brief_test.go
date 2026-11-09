package brief

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayaSuryaOolio/conveyor/internal/store"
	"github.com/JayaSuryaOolio/conveyor/internal/workspace"
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
	// The task is a build step of epic os-4, whose root is in stage build on flow 1.
	root, err := store.CreateTask(db, store.Task{Title: "Auth", Status: store.StatusTodo}, []store.TaskRepo{{RepoID: r.ID, IsPrimary: true, Branch: "auth"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"epic", "epic:os-4", "epic_stage:build"} {
		store.AddLabel(db, root.ID, l)
	}
	if _, err := store.StartFlow(db, root.ID); err != nil {
		t.Fatal(err)
	}
	tk, err := store.CreateTask(db, store.Task{Title: "Add login", Description: "Users can log in.", Status: store.StatusTodo, ParentID: &root.ID},
		[]store.TaskRepo{{RepoID: r.ID, IsPrimary: true, Branch: "login"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"epic:os-4", "epic_kind:build", "repo:api", "skill:tdd", "skill:grilling"} {
		store.AddLabel(db, tk.ID, l)
	}
	wt, pr := "/ws/CV-1A/api", "https://example.com/pr/1"
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
	if path != filepath.Join(dir, "workspaces", "CV-1A", "BRIEF.md") {
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

	// Repo learnings land in the workspace CLAUDE.md and AGENTS.md, which go away when they do.
	wsClaude := filepath.Join(dir, "workspaces", "CV-1A", "CLAUDE.md")
	wsAgents := filepath.Join(dir, "workspaces", "CV-1A", "AGENTS.md")
	if _, err := os.Stat(wsClaude); !os.IsNotExist(err) {
		t.Fatalf("CLAUDE.md without learnings: %v", err)
	}
	if _, err := os.Stat(wsAgents); !os.IsNotExist(err) {
		t.Fatalf("AGENTS.md without learnings: %v", err)
	}
	lf := workspace.LearningsFile(dir, "api")
	os.MkdirAll(filepath.Dir(lf), 0o755)
	os.WriteFile(lf, []byte("- use make test\n"), 0o644)
	Render(db, dir, tk)
	if b, _ := os.ReadFile(wsClaude); string(b) != "# api learnings\n\n- use make test\n\n" {
		t.Fatalf("CLAUDE.md %q", b)
	}
	if b, _ := os.ReadFile(wsAgents); string(b) != "# api learnings\n\n- use make test\n\n" {
		t.Fatalf("AGENTS.md %q", b)
	}
	os.WriteFile(lf, nil, 0o644)
	Render(db, dir, tk)
	if _, err := os.Stat(wsClaude); !os.IsNotExist(err) {
		t.Fatalf("stale CLAUDE.md: %v", err)
	}
	if _, err := os.Stat(wsAgents); !os.IsNotExist(err) {
		t.Fatalf("stale AGENTS.md: %v", err)
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
	if strings.Contains(got, "Labels:") {
		t.Fatal("no labels, no Labels line")
	}
	tk.Labels = []string{"epic_kind:grilling", "skill:grill-me"}
	if got, _ = Build(db, tk); !strings.Contains(got, "Kind grilling: ") || !strings.Contains(got, "open no PR") ||
		!strings.Contains(got, "Load these skills before starting: grill-me.") || strings.Contains(got, "Load the epic skill") {
		t.Fatalf("kind/skills lines:\n%s", got)
	}
	d := []store.Comment{{Author: "human", Body: "a"}}
	if got := FreshPrompt(tk, "/b.md", d, "{key}|{title}|{brief}|{comments}"); got != "CV-3|T|/b.md|- [human] a" {
		t.Errorf("custom fresh %q", got)
	}
	if got := ResumePrompt(tk, "/b.md", nil, ""); !strings.Contains(got, "no new comments") {
		t.Errorf("empty delta prompt = %q", got)
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

