package learning

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayaSuryaOolio/conveyor/internal/core"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

func setup(t *testing.T) (*core.Service, []string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := &core.Service{DB: db}
	var keys []string
	for _, n := range []string{"a", "b"} {
		r, _ := store.AddRepo(db, store.Repo{Name: n, Path: "/x/" + n, DefaultBranch: "main"})
		tk, err := svc.CreateTask(core.NewTaskInput{Title: "src " + n, Repos: []string{n}})
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tk.Key)
		for _, body := range []string{n + "1", n + "2"} {
			db.Exec(`INSERT INTO learnings (task_id, repo_id, body, status, created_at) VALUES (?,?,?,'proposed','t')`, tk.ID, r.ID, body)
		}
	}
	db.Exec(`INSERT INTO learnings (body, status, created_at) VALUES ('mine','proposed','t')`)
	return svc, keys
}

func TestTransitions(t *testing.T) {
	svc, _ := setup(t)
	db := svc.DB
	if err := Accept(db, 1, ""); err != nil {
		t.Fatal(err)
	}
	l, _ := store.GetLearning(db, 1)
	if l.Status != "accepted" || *l.Target != "CLAUDE.md" {
		t.Fatalf("%+v", l)
	}
	Accept(db, 2, "docs/AGENTS.md")
	Edit(db, 2, " better ")
	Reject(db, 3)
	l2, _ := store.GetLearning(db, 2)
	l3, _ := store.GetLearning(db, 3)
	if *l2.Target != "docs/AGENTS.md" || l2.Body != "better" || l3.Status != "rejected" {
		t.Fatalf("%+v %+v", l2, l3)
	}
	if ps, _ := List(db, "proposed"); len(ps) != 2 {
		t.Fatalf("proposed %d", len(ps))
	}
	if Edit(db, 1, " ") == nil {
		t.Fatal("empty edit should fail")
	}
	if _, err := Apply(svc, "a"); err != nil {
		t.Fatal(err)
	}
	if Reject(db, 1) == nil || Accept(db, 1, "") == nil || Edit(db, 1, "x") == nil {
		t.Fatal("applied learnings are immutable")
	}
}

func TestApplyGroupsByRepo(t *testing.T) {
	svc, src := setup(t)
	for id := 1; id <= 5; id++ {
		if id != 3 {
			Accept(svc.DB, int64(id), "")
		}
	}
	Accept(svc.DB, 4, "docs/X.md")
	keys, err := Apply(svc, "")
	if err != nil || len(keys) != 2 {
		t.Fatalf("%v %v", keys, err)
	}
	ta, _ := store.GetTaskByKey(svc.DB, keys[0])
	tb, _ := store.GetTaskByKey(svc.DB, keys[1])
	if ta.Title != "Apply learnings to a" || ta.Status != store.StatusTodo || ta.Source != "learning" || ta.Repos[0].RepoName != "a" {
		t.Fatalf("%+v", ta)
	}
	for _, want := range []string{"a1 (target: CLAUDE.md, from " + src[0] + ")", "a2", "open one PR"} {
		if !strings.Contains(ta.Description, want) {
			t.Errorf("a description missing %q:\n%s", want, ta.Description)
		}
	}
	if !strings.Contains(tb.Description, "b2 (target: docs/X.md, from "+src[1]+")") || strings.Contains(tb.Description, "a1") || strings.Contains(tb.Description, "mine") {
		t.Fatalf("b description:\n%s", tb.Description)
	}
	applied, _ := List(svc.DB, "applied")
	if len(applied) != 3 || *applied[0].AppliedTaskID != ta.ID {
		t.Fatalf("%+v", applied)
	}
	if p, _ := Personal(svc.DB); len(p) != 1 || p[0].Body != "mine" {
		t.Fatalf("personal %+v", p)
	}
	if again, _ := Apply(svc, ""); len(again) != 0 {
		t.Fatalf("second apply made %v", again)
	}
	if _, err := Apply(svc, "nope"); err == nil {
		t.Fatal("unknown repo")
	}
}

// A failure after the task is created (learnings still accepted) must not make a retry create a second task.
func TestApplyRetryAfterPartialFailure(t *testing.T) {
	svc, _ := setup(t)
	Accept(svc.DB, 1, "")
	Accept(svc.DB, 2, "")
	// Simulate Apply dying right after CreateTask: make marking fail.
	svc.DB.Exec(`CREATE TRIGGER fail_mark BEFORE UPDATE ON learnings BEGIN SELECT RAISE(ABORT, 'boom'); END`)
	if _, err := Apply(svc, "a"); err == nil {
		t.Fatal("expected failure")
	}
	svc.DB.Exec(`DROP TRIGGER fail_mark`)
	keys, err := Apply(svc, "a")
	if err != nil || len(keys) != 1 {
		t.Fatalf("%v %v", keys, err)
	}
	var n int
	svc.DB.QueryRow(`SELECT COUNT(*) FROM tasks WHERE source='learning'`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d learning tasks, want 1", n)
	}
	if applied, _ := List(svc.DB, "applied"); len(applied) != 2 {
		t.Fatalf("applied %d", len(applied))
	}
}

func TestDynamicTargetResolutionAgy(t *testing.T) {
	svc, _ := setup(t)
	// Set default runner to agy
	if err := store.SetSetting(svc.DB, "default_runner", "agy"); err != nil {
		t.Fatal(err)
	}

	// Learning without target accepted should resolve to AGENTS.md
	if err := Accept(svc.DB, 1, ""); err != nil {
		t.Fatal(err)
	}
	l, err := store.GetLearning(svc.DB, 1)
	if err != nil {
		t.Fatal(err)
	}
	if l.Target == nil || *l.Target != "AGENTS.md" {
		t.Fatalf("expected AGENTS.md, got %v", l.Target)
	}

	// Apply learnings to repo 'a'
	keys, err := Apply(svc, "a")
	if err != nil || len(keys) != 1 {
		t.Fatalf("Apply: %v, keys: %v", err, keys)
	}
	tk, err := store.GetTaskByKey(svc.DB, keys[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tk.Description, "(target: AGENTS.md, from") {
		t.Errorf("task description should target AGENTS.md:\n%s", tk.Description)
	}
	if tk.Runner == nil || *tk.Runner != "agy" {
		t.Errorf("expected task runner agy, got %v", tk.Runner)
	}
}

