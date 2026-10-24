package ui

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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

func TestOpenLogRotates(t *testing.T) {
	p := filepath.Join(t.TempDir(), "conveyor.log")
	os.WriteFile(p, make([]byte, maxLog+1), 0o644)
	f, err := openLog(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if fi, _ := os.Stat(p); fi.Size() != 0 {
		t.Fatalf("log not rotated: %d bytes", fi.Size())
	}
	if fi, err := os.Stat(p + ".1"); err != nil || fi.Size() != maxLog+1 {
		t.Fatalf("old log not kept: %v", err)
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

func TestListCLIAgentsAndSetTaskRunner(t *testing.T) {
	a, _ := newApp(t)
	agents := a.ListCLIAgents()
	if len(agents) < 2 {
		t.Fatalf("expected at least 2 CLI agents (claude and agy), got %d", len(agents))
	}
	hasClaude, hasAgy := false, false
	for _, ag := range agents {
		if ag.ID == "claude" {
			hasClaude = true
		}
		if ag.ID == "agy" {
			hasAgy = true
		}
	}
	if !hasClaude || !hasAgy {
		t.Fatalf("expected claude and agy in agents: %+v", agents)
	}

	// Test task creation with runner
	key, err := a.CreateTask(core.NewTaskInput{Title: "TaskWithRunner", Repos: []string{"a"}, Runner: "agy"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := a.GetTask(key)
	if err != nil {
		t.Fatal(err)
	}
	if d.Task.Runner == nil || *d.Task.Runner != "agy" {
		t.Fatalf("expected runner agy, got %v", d.Task.Runner)
	}

	// Test SetTaskRunner
	if err := a.SetTaskRunner(key, "claude"); err != nil {
		t.Fatal(err)
	}
	d, _ = a.GetTask(key)
	if d.Task.Runner == nil || *d.Task.Runner != "claude" {
		t.Fatalf("expected runner claude, got %v", d.Task.Runner)
	}
}
