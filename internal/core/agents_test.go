package core

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

func TestAgents(t *testing.T) {
	s := newSvc(t)
	home, repo := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	store.RemoveRepo(s.DB, "a")
	if _, err := store.AddRepo(s.DB, store.Repo{Name: "a", Path: repo, DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAgent("rev-1", "Reviews: code", "Be strict.", "a", "opus"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAgent("me", "Mine", "Hi", "user", ""); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{{"Bad Name", "d", "a"}, {"rev-1", "d", "a"}, {"x", "", "a"}, {"x", "d", "nope"}} {
		if s.AddAgent(bad[0], bad[1], "p", bad[2], "") == nil {
			t.Errorf("%v should fail", bad)
		}
	}
	os.WriteFile(filepath.Join(repo, ".claude", "agents", "noname.md"), []byte("no frontmatter"), 0o644)
	got, err := s.ListAgents([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Agent{{Name: "rev-1", Description: "Reviews: code", Scope: "a", Model: "opus", Skills: []string{}}, {Name: "me", Description: "Mine", Scope: "user", Skills: []string{}}}
	if len(got) != 2 || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if err := s.SetAgentModel("user", "me", "haiku"); err != nil || AgentModel("me") != "haiku" || AgentModel("rev-1", repo) != "opus" {
		t.Fatalf("set model: %v", err)
	}
	if s.SetAgentModel("user", "nope", "x") == nil {
		t.Fatal("missing agent should fail")
	}
	tk, _ := s.CreateTask(NewTaskInput{Title: "t", Repos: []string{"a"}, Agent: "rev-1"})
	if tk.Agent == nil || *tk.Agent != "rev-1" {
		t.Fatalf("%+v", tk.Agent)
	}

	// Add a run with session_id to tk
	sessID := "conv-123"
	r, err := store.CreateRun(s.DB, store.Run{TaskID: tk.ID, Kind: "auto", Mode: "fresh", Status: "finished", SessionID: &sessID})
	if err != nil {
		t.Fatal(err)
	}
	if r.SessionID == nil || *r.SessionID != "conv-123" {
		t.Fatal("session not set on run")
	}

	if err := s.SetTaskAgent(tk.Key, "other-agent"); err != nil {
		t.Fatal(err)
	}
	if tk, _ = store.GetTaskByKey(s.DB, tk.Key); tk.Agent == nil || *tk.Agent != "other-agent" {
		t.Fatal("agent not updated")
	}
	// Verify session IDs were cleared on runs
	runs, _ := store.ListRuns(s.DB, tk.ID)
	if len(runs) != 1 || runs[0].SessionID != nil {
		t.Fatalf("expected session_id to be cleared on runs after agent change, got %+v", runs)
	}

	if err := s.SetTaskAgent(tk.Key, ""); err != nil {
		t.Fatal(err)
	}
	if tk, _ = store.GetTaskByKey(s.DB, tk.Key); tk.Agent != nil {
		t.Fatal("agent not cleared")
	}
}

func TestSetFrontmatterModel(t *testing.T) {
	for _, c := range []struct{ in, model, want string }{
		{"---\nname: a\n---\nbody\nmodel: x\n", "opus", "---\nname: a\nmodel: opus\n---\nbody\nmodel: x\n"},
		{"---\nname: a\nmodel: haiku\ndescription: d\n---\nb", "opus", "---\nname: a\nmodel: opus\ndescription: d\n---\nb"},
		{"---\nname: a\nmodel: haiku\ndescription: d\n---\nb", "", "---\nname: a\ndescription: d\n---\nb"},
		{"---\nname: a\n---\nb", "", "---\nname: a\n---\nb"},
	} {
		got, err := setFrontmatterModel([]byte(c.in), c.model)
		if err != nil || string(got) != c.want {
			t.Errorf("%q -> %q, %v", c.in, got, err)
		}
	}
	if _, err := setFrontmatterModel([]byte("no fm"), "x"); err == nil {
		t.Error("want error")
	}
}
