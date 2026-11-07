package ui

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JayaSuryaOolio/conveyor/internal/agy"
	"github.com/JayaSuryaOolio/conveyor/internal/core"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

func newApp(t *testing.T) (*App, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "c.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store.AddRepo(db, store.Repo{Name: "a", Path: "/x/a", DefaultBranch: "main"})
	return &App{svc: &core.Service{DB: db}}, path
}

func TestListTasksAndGetTask(t *testing.T) {
	a, _ := newApp(t)
	key, err := a.CreateTask(core.NewTaskInput{Title: "One", Repos: []string{"a"}, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	if cs, _ := a.ListTasks(); len(cs) != 1 || cs[0].LastSummary != "" || cs[0].Repos[0] != "a" || cs[0].Status != "todo" {
		t.Fatalf("no-run card: %+v", cs)
	}
	tk, _ := store.GetTaskByKey(a.svc.DB, key)
	sum := "did it"
	run, _ := store.CreateRun(a.svc.DB, store.Run{TaskID: tk.ID, Kind: "auto", Mode: "fresh", Status: "finished", CtxPeak: 31000})
	run.Summary = &sum
	store.UpdateRun(a.svc.DB, run)
	if err := a.AddComment(key, "hi", ""); err != nil {
		t.Fatal(err)
	}
	cs, _ := a.ListTasks()
	if cs[0].LastSummary != "did it" || cs[0].CtxPeak != 31000 {
		t.Fatalf("card: %+v", cs[0])
	}
	d, err := a.GetTask(key)
	if err != nil || d.Task.Key != key || len(d.Comments) != 1 || len(d.Runs) != 1 || len(d.Task.Repos) != 1 {
		t.Fatalf("detail: %+v %v", d, err)
	}
	if _, err := a.GetTask("NOPE-1"); err == nil {
		t.Fatal("want error")
	}
}

func TestPollEmitsOnExternalWrite(t *testing.T) {
	a, path := newApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var n atomic.Int32
	go pollDataVersion(ctx, path, 10*time.Millisecond, func() { n.Add(1) })
	time.Sleep(100 * time.Millisecond)
	if n.Load() != 0 {
		t.Fatal("emitted without a write")
	}
	a.CreateTask(core.NewTaskInput{Title: "x", Repos: []string{"a"}})
	deadline := time.Now().Add(2 * time.Second)
	for n.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n.Load() == 0 {
		t.Fatal("no emit after write")
	}
}

func TestAutomationCRUDAndDescribeSchedule(t *testing.T) {
	a, _ := newApp(t)
	x := store.Automation{Name: "n", Schedule: "0 9 * * 1-5", ActionKind: "cli", ActionArg: `{"args":["list"]}`}
	if err := a.AddAutomation(x); err != nil {
		t.Fatal(err)
	}
	if a.AddAutomation(x) == nil {
		t.Fatal("duplicate name accepted")
	}
	if a.AddAutomation(store.Automation{Name: "bad", Schedule: "nope", ActionKind: "cli", ActionArg: "{}"}) == nil {
		t.Fatal("bad schedule accepted")
	}
	got, _ := store.GetAutomation(a.svc.DB, "n")
	res := "ok"
	got.LastResult = &res
	store.UpdateAutomation(a.svc.DB, got)
	x.Schedule, x.Precheck = "*/5 * * * *", "true"
	if err := a.UpdateAutomation(x); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetAutomation(a.svc.DB, "n")
	if got.Schedule != "*/5 * * * *" || got.Precheck != "true" || got.LastResult == nil || !got.Enabled {
		t.Fatalf("update: %+v", got)
	}
	x.Name = "missing"
	if a.UpdateAutomation(x) == nil {
		t.Fatal("update of missing automation succeeded")
	}
	if err := a.DeleteAutomation("n"); err != nil {
		t.Fatal(err)
	}
	if a.DeleteAutomation("n") == nil {
		t.Fatal("double delete succeeded")
	}
	if i := a.DescribeSchedule("0 9 * * 1-5"); !i.Valid || len(i.Next) != 3 || i.Description != "Every weekday at 09:00" {
		t.Fatalf("describe: %+v", i)
	}
	if i := a.DescribeSchedule("x"); i.Valid || i.Error == "" {
		t.Fatalf("describe invalid: %+v", i)
	}
}

func TestSetLabelSwapsPrefixOnly(t *testing.T) {
	a, _ := newApp(t)
	key, _ := a.CreateTask(core.NewTaskInput{Title: "k", Repos: []string{"a"}})
	for _, l := range []string{"epic:os", "epic_kind:build", "plain"} {
		a.AddLabel(key, l)
	}
	if err := a.SetLabel(key, "epic_kind", "research"); err != nil {
		t.Fatal(err)
	}
	tk, _ := store.GetTaskByKey(a.svc.DB, key)
	ls, _ := store.TaskLabels(a.svc.DB, tk.ID)
	if got := strings.Join(ls, ","); !strings.Contains(got, "epic_kind:research") || strings.Contains(got, "epic_kind:build") || !strings.Contains(got, "epic:os") || !strings.Contains(got, "plain") {
		t.Fatalf("labels: %v", ls)
	}
	if a.SetLabel(key, "epic_kind", "") == nil {
		t.Fatal("empty value accepted")
	}
}

func TestApp_TrustsWorkspaces(t *testing.T) {
	tmp := t.TempDir()
	settingsFile := filepath.Join(tmp, "gemini-settings.json")
	t.Setenv("CONVEYOR_AGY_SETTINGS", settingsFile)

	a, _ := newApp(t)
	a.svc.Home = filepath.Join(tmp, ".conveyor")

	if err := a.SetSetting("default_runner", "agy"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	trusted, err := agy.GetTrustedWorkspaces()
	if err != nil {
		t.Fatalf("GetTrustedWorkspaces: %v", err)
	}

	hasA := false
	hasWorkspaces := false
	for _, p := range trusted {
		if p == "/x/a" {
			hasA = true
		}
		if p == filepath.Join(a.svc.Home, "workspaces") {
			hasWorkspaces = true
		}
	}
	if !hasA {
		t.Errorf("repo /x/a not in trusted: %v", trusted)
	}
	if !hasWorkspaces {
		t.Errorf("workspaces not in trusted: %v", trusted)
	}
}
