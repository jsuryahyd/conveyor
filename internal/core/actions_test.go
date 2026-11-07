package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

func actionTask(t *testing.T, s *Service) store.Task {
	t.Helper()
	s.Home, s.ProjectsDir = t.TempDir(), t.TempDir()
	tk, err := s.CreateTask(NewTaskInput{Title: "x", Repos: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := range tk.Repos {
		p := filepath.Join(s.Home, "ws", tk.Repos[i].RepoName)
		tk.Repos[i].WorktreePath = &p
		if err := store.UpdateTaskRepo(s.DB, tk.Repos[i]); err != nil {
			t.Fatal(err)
		}
	}
	return tk
}

func kinds(as []Action) string {
	var k []string
	for _, a := range as {
		k = append(k, a.Kind+":"+a.Repo)
	}
	return strings.Join(k, ",")
}

func TestActionsByState(t *testing.T) {
	s := newSvc(t)
	tk := actionTask(t, s)
	check := func(want string) {
		t.Helper()
		as, err := s.Actions(tk.Key)
		if err != nil {
			t.Fatal(err)
		}
		if got := kinds(as); got != want {
			t.Fatalf("got %s want %s", got, want)
		}
	}
	base := "fresh:a,shell:a,shell:b"
	check(base) // no runs
	bg, sess := "bg1", "sess1"
	r, _ := store.CreateRun(s.DB, store.Run{TaskID: tk.ID, Kind: "auto", Mode: "fresh", Status: "running", BgID: &bg})
	check("attach:a," + base)
	r.Status, r.SessionID = "finished", &sess
	store.UpdateRun(s.DB, r)
	check("resume:a," + base + ",resume:" + strconv.FormatInt(r.ID, 10) + ":a")
	r.SessionID = nil
	store.UpdateRun(s.DB, r)
	check(base)
	// an active run that is no longer the latest keeps its attach icon
	r.Status, r.SessionID = "running", &sess
	store.UpdateRun(s.DB, r)
	store.CreateRun(s.DB, store.Run{TaskID: tk.ID, Kind: "interactive", Mode: "fresh", Status: "finished"})
	check(base + ",attach:" + strconv.FormatInt(r.ID, 10) + ":a")
	if _, err := os.Stat(filepath.Join(s.Home, "claude-settings.json")); err != nil {
		t.Fatal("settings not written")
	}
}

func TestActionsNoWorkspace(t *testing.T) {
	s := newSvc(t)
	tk, _ := s.CreateTask(NewTaskInput{Title: "x", Repos: []string{"a"}})
	if as, err := s.Actions(tk.Key); err != nil || len(as) != 0 {
		t.Fatalf("%v %v", as, err)
	}
	if _, err := s.FindAction(tk.Key, "shell", ""); err == nil {
		t.Fatal("want error")
	}
}

func TestSessions(t *testing.T) {
	s := newSvc(t)
	tk := actionTask(t, s)
	wt := *tk.Repos[0].WorktreePath
	dir := filepath.Join(s.ProjectsDir, nonAlnumPath.ReplaceAllString(wt, "-"))
	os.MkdirAll(dir, 0o755)
	long := strings.Repeat("é", 100)
	os.WriteFile(filepath.Join(dir, "m1.jsonl"), []byte(
		`{"type":"summary"}`+"\n"+
			`{"type":"user","message":{"role":"user","content":"<command-name>/x</command-name>"}}`+"\n"+
			`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"`+long+`"}]}}`+"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "run1.jsonl"), []byte(`{"type":"user","message":{"content":"hi"}}`+"\n"), 0o644)
	sid := "run1"
	store.CreateRun(s.DB, store.Run{TaskID: tk.ID, Kind: "auto", Mode: "fresh", Status: "finished", SessionID: &sid})
	ss, err := s.Sessions(tk.Key)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %v", ss, err)
	}
	if ss[0].ID != "m1" || len([]rune(ss[0].Prompt)) != 80 || ss[0].Action.Kind != "resume" || !strings.Contains(ss[0].Action.Cmd, "--resume 'm1'") {
		t.Fatalf("%+v", ss[0])
	}
	if a, err := s.FindAction(tk.Key, "resume:m1", ""); err != nil || a.Cwd != wt {
		t.Fatalf("%+v %v", a, err)
	}
}

func TestActionsUseBin(t *testing.T) {
	s := newSvc(t)
	tk := actionTask(t, s)
	s.Bin = "/opt/cli/conveyor"
	if _, err := s.Actions(tk.Key); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(s.Home, "claude-settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "/opt/cli/conveyor") {
		t.Fatalf("hooks do not call Bin: %s", b)
	}
}

func TestActionsAgyAttach(t *testing.T) {
	s := newSvc(t)
	tk := actionTask(t, s)
	agyRunner := "agy"
	tk.Runner = &agyRunner
	store.UpdateTask(s.DB, tk)

	bg, sess := "999999", "conv-uuid-456"
	_, _ = store.CreateRun(s.DB, store.Run{TaskID: tk.ID, Kind: "auto", Mode: "fresh", Status: "running", BgID: &bg, SessionID: &sess})
	as, err := s.Actions(tk.Key)
	if err != nil {
		t.Fatal(err)
	}
	var attachCmd string
	for _, a := range as {
		if a.Kind == "attach" {
			attachCmd = a.Cmd
		}
	}
	if strings.Contains(attachCmd, "kill -TERM") {
		t.Fatalf("agy attach should not kill process: %s", attachCmd)
	}
}
