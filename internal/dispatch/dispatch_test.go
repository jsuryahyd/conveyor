package dispatch

import (
	"database/sql"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayaSuryaOolio/conveyor/internal/runner"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=a@b", "-c", "user.name=t"}, args...)...)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// fakeClaude puts a `claude` script on PATH. Each call appends "cwd|argv(\x1f-joined)" to the log.
// `agents` prints <dir>/agents.json (default []); `--bg` prints a bg id; a <dir>/fail file makes it exit 1 printing its content.
func fakeClaude(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	script := `#!/bin/bash
d="$(dirname "$0")"
printf '%s|' "$PWD" >> "$d/log"
printf '%s\x1f' "$@" >> "$d/log"
printf '\n' >> "$d/log"
if [ "$1" = agents ]; then cat "$d/agents.json" 2>/dev/null || echo '[]'; exit 0; fi
if [ -f "$d/fail" ]; then cat "$d/fail"; exit 1; fi
if [ "$1" = --bg ]; then echo "backgrounded · abcd0001"; fi
`
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// launches returns the logged claude calls that start or resume a session.
func launches(t *testing.T, dir string) [][]string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, "log"))
	var out [][]string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l == "" {
			continue
		}
		cwd, args, _ := strings.Cut(l, "|")
		a := append([]string{cwd}, strings.Split(strings.TrimSuffix(args, "\x1f"), "\x1f")...)
		if a[1] == "--bg" {
			out = append(out, a)
		}
	}
	return out
}

type env struct {
	db   *sql.DB
	d    *Dispatcher
	fake string
	repo store.Repo
	root string
	now  time.Time
}

func setup(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	p, o := filepath.Join(root, "api"), filepath.Join(root, "api-origin.git")
	os.MkdirAll(p, 0o755)
	git(t, p, "init", "-b", "main")
	git(t, p, "commit", "--allow-empty", "-m", "init")
	git(t, root, "init", "--bare", o)
	git(t, p, "remote", "add", "origin", o)
	git(t, p, "push", "origin", "main")
	repo, err := store.AddRepo(db, store.Repo{Name: "api", Path: p, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{db: db, fake: fakeClaude(t), repo: repo, root: root, now: time.Now()}
	e.d = &Dispatcher{DB: db, Home: filepath.Join(root, "home"), ConveyorBin: "/bin/conveyor",
		Now: func() time.Time { return e.now }, Log: log.New(io.Discard, "", 0)}
	return e
}

func (e *env) task(t *testing.T, title string, prio int) store.Task {
	t.Helper()
	tk, err := store.CreateTask(e.db, store.Task{Title: title, Status: store.StatusTodo, Priority: prio},
		[]store.TaskRepo{{RepoID: e.repo.ID, IsPrimary: true, Branch: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func (e *env) get(t *testing.T, id int64) store.Task {
	t.Helper()
	tk, err := store.GetTask(e.db, id)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func (e *env) tick(t *testing.T) {
	t.Helper()
	if err := e.d.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func real(p string) string { r, _ := filepath.EvalSymlinks(p); return r }

func TestLaunchFresh(t *testing.T) {
	e := setup(t)
	tk := e.task(t, "Do it", 0)
	e.tick(t)
	got := e.get(t, tk.ID)
	if got.Status != store.StatusRunning {
		t.Fatalf("status %s", got.Status)
	}
	ls := launches(t, e.fake)
	if len(ls) != 1 {
		t.Fatalf("launches: %v", ls)
	}
	a := ls[0]
	wantCwd := filepath.Join(e.d.Home, "workspaces", tk.Key, "api")
	if real(a[0]) != real(wantCwd) {
		t.Errorf("cwd %s, want %s", a[0], wantCwd)
	}
	joined := strings.Join(a, " ")
	for _, w := range []string{"--bg You are working on Conveyor task " + tk.Key + ": Do it.", "--name " + tk.Key, "--permission-mode bypassPermissions", "--settings " + filepath.Join(e.d.Home, "claude-settings.json"), "--strict-mcp-config"} {
		if !strings.Contains(joined, w) {
			t.Errorf("argv missing %q: %q", w, joined)
		}
	}
	run, err := store.LatestRun(e.db, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.BgID == nil || *run.BgID != "abcd0001" || run.Mode != "fresh" || run.Kind != "auto" || run.Status != "launching" {
		t.Errorf("run %+v", run)
	}
	if _, err := os.Stat(filepath.Join(e.d.Home, "workspaces", tk.Key, "BRIEF.md")); err != nil {
		t.Error(err)
	}
	e.tick(t) // running with an active run: no second launch
	if n := len(launches(t, e.fake)); n != 1 {
		t.Errorf("relaunched: %d", n)
	}
}

func TestBlockedTaskWaits(t *testing.T) {
	e := setup(t)
	blocker := e.task(t, "first", 0)
	waiting := e.task(t, "second", 0)
	if err := store.AddLink(e.db, blocker.ID, waiting.ID, store.LinkBlocks); err != nil {
		t.Fatal(err)
	}
	// blocker is todo too, so it launches; the blocked task must not.
	e.tick(t)
	if e.get(t, waiting.ID).Status != store.StatusTodo || e.get(t, blocker.ID).Status != store.StatusRunning {
		t.Fatal("blocked task launched, or blocker did not")
	}
	if _, err := store.UpdateTaskStatus(e.db, blocker.ID, store.StatusReview, store.ActorSystem); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateTaskStatus(e.db, blocker.ID, store.StatusDone, store.ActorHuman); err != nil {
		t.Fatal(err)
	}
	e.tick(t)
	if e.get(t, waiting.ID).Status != store.StatusRunning {
		t.Fatal("task did not launch once its blocker was done")
	}
}

func TestDebounce(t *testing.T) {
	e := setup(t)
	tk := e.task(t, "t", 0)
	if _, err := store.AddComment(e.db, store.Comment{TaskID: tk.ID, Author: store.AuthorHuman, Body: "hi"}); err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	e.now = base.Add(5 * time.Second)
	e.tick(t)
	if e.get(t, tk.ID).Status != store.StatusTodo {
		t.Fatal("launched inside the debounce window")
	}
	e.now = base.Add(25 * time.Second)
	e.tick(t)
	if e.get(t, tk.ID).Status != store.StatusRunning {
		t.Fatal("did not launch after the debounce window")
	}
	if raw, _ := os.ReadFile(filepath.Join(e.fake, "log")); !strings.Contains(string(raw), "\n- [human] hi") {
		t.Error("prompt lacks the comment delta")
	}
	if left, _ := store.UnconsumedComments(e.db, tk.ID); len(left) != 0 {
		t.Error("comment not consumed")
	}
}

func TestResumeVsFresh(t *testing.T) {
	for _, tc := range []struct {
		peak   int64
		resume bool
	}{{1000, true}, {100000, false}} {
		e := setup(t)
		tk := e.task(t, "t", 0)
		r, _ := store.CreateRun(e.db, store.Run{TaskID: tk.ID, Kind: "auto", Mode: "fresh", Status: "launching"})
		sess, bg := "sess-1", "oldbg001"
		r.SessionID, r.BgID, r.Status, r.CtxPeak = &sess, &bg, "finished", tc.peak
		if err := store.UpdateRun(e.db, r); err != nil {
			t.Fatal(err)
		}
		store.AddComment(e.db, store.Comment{TaskID: tk.ID, Author: store.AuthorHuman, Body: "more"})
		e.now = time.Now().Add(time.Minute)
		e.tick(t)
		a := strings.Join(launches(t, e.fake)[0], " ")
		if tc.resume {
			if !strings.Contains(a, "--bg --resume sess-1 New comments on "+tk.Key) || strings.Contains(a, "--permission-mode") {
				t.Errorf("peak %d: not a bare resume: %q", tc.peak, a)
			}
		} else if strings.Contains(a, "--resume") || !strings.Contains(a, "You are working on") {
			t.Errorf("peak %d: not fresh: %q", tc.peak, a)
		}
		run, _ := store.LatestRun(e.db, tk.ID)
		if tc.resume && (run.Mode != "resume" || run.SessionID == nil || *run.SessionID != sess) {
			t.Errorf("resume run %+v", run)
		}
	}
}

func TestMaxParallel(t *testing.T) {
	e := setup(t)
	store.SetSetting(e.db, "max_parallel", "1")
	lo, hi := e.task(t, "low", 0), e.task(t, "high", 5)
	e.tick(t)
	if e.get(t, hi.ID).Status != store.StatusRunning || e.get(t, lo.ID).Status != store.StatusTodo {
		t.Fatalf("hi=%s lo=%s", e.get(t, hi.ID).Status, e.get(t, lo.ID).Status)
	}
	e.tick(t)
	if n := len(launches(t, e.fake)); n != 1 {
		t.Errorf("launches %d", n)
	}
}

func TestLaunchFailures(t *testing.T) {
	e := setup(t)
	store.SetSetting(e.db, "max_consecutive_failures", "2")
	os.WriteFile(filepath.Join(e.fake, "fail"), []byte("Workspace not trusted\n"), 0o644)
	tk := e.task(t, "t", 0)
	e.tick(t)
	got := e.get(t, tk.ID)
	if got.Status != store.StatusTodo || got.ConsecutiveFailures != 1 {
		t.Fatalf("after 1: %s/%d", got.Status, got.ConsecutiveFailures)
	}
	run, _ := store.LatestRun(e.db, tk.ID)
	if run.Status != "failed" || run.Error == nil || !strings.Contains(*run.Error, "not trusted") {
		t.Fatalf("run %+v", run)
	}
	e.tick(t)
	got = e.get(t, tk.ID)
	if got.Status != store.StatusFailed || got.ConsecutiveFailures != 2 {
		t.Fatalf("after 2: %s/%d", got.Status, got.ConsecutiveFailures)
	}
	cs, _ := store.ListComments(e.db, tk.ID)
	if len(cs) == 0 || cs[len(cs)-1].Author != store.AuthorSystem || !strings.Contains(cs[len(cs)-1].Body, "open "+e.repo.Path+" in Claude once") {
		t.Fatalf("comments %+v", cs)
	}
}

func TestFailedLaunchKeepsCommentDelta(t *testing.T) {
	e := setup(t)
	tk := e.task(t, "t", 0)
	store.AddComment(e.db, store.Comment{TaskID: tk.ID, Author: store.AuthorHuman, Body: "please do X"})
	fail := filepath.Join(e.fake, "fail")
	os.WriteFile(fail, []byte("boom\n"), 0o644)
	e.now = time.Now().Add(time.Minute)
	e.tick(t)
	if left, _ := store.UnconsumedComments(e.db, tk.ID); len(left) != 1 {
		t.Fatalf("comment lost after failed launch: %d unconsumed", len(left))
	}
	os.Remove(fail)
	e.tick(t)
	if raw, _ := os.ReadFile(filepath.Join(e.fake, "log")); !strings.Contains(string(raw), "\n- [human] please do X") {
		t.Error("retry prompt lacks the comment")
	}
	if left, _ := store.UnconsumedComments(e.db, tk.ID); len(left) != 0 {
		t.Error("comment not consumed by the successful run")
	}
}

func TestStatusFailureReleasesComments(t *testing.T) {
	e := setup(t)
	tk := e.task(t, "t", 0)
	c, _ := store.AddComment(e.db, store.Comment{TaskID: tk.ID, Author: store.AuthorHuman, Body: "please do X"})
	// Another actor moves the task off todo after the dispatcher picked it, so todo->running fails.
	if _, err := store.UpdateTaskStatus(e.db, tk.ID, store.StatusRunning, store.ActorSystem); err != nil {
		t.Fatal(err)
	}
	run, err := e.d.start(t.Context(), tk, []store.Comment{c})
	if err == nil {
		t.Fatal("start succeeded despite the status conflict")
	}
	if left, _ := store.UnconsumedComments(e.db, tk.ID); len(left) != 1 {
		t.Fatalf("comment stays consumed by a run that never launched: %d unconsumed", len(left))
	}
	if err := e.d.fail(tk, run, err); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetRun(e.db, run.ID); got.Status != "failed" {
		t.Fatalf("run left %s", got.Status)
	}
}

func TestReapVanished(t *testing.T) {
	e := setup(t)
	tk := e.task(t, "t", 0)
	e.tick(t) // launches, bg abcd0001
	run, _ := store.LatestRun(e.db, tk.ID)

	os.WriteFile(filepath.Join(e.fake, "agents.json"), []byte(`[{"id":"abcd0001"}]`), 0o644)
	e.now = time.Now().Add(time.Minute)
	e.tick(t)
	if e.get(t, tk.ID).Status != store.StatusRunning {
		t.Fatal("reaped a live session")
	}

	os.WriteFile(filepath.Join(e.fake, "agents.json"), []byte(`[]`), 0o644)
	e.now = time.Now().Add(5 * time.Second) // younger than the grace period
	e.tick(t)
	if e.get(t, tk.ID).Status != store.StatusRunning {
		t.Fatal("reaped inside the grace period")
	}

	e.now = time.Now().Add(time.Minute)
	e.tick(t)
	got, _ := store.GetRun(e.db, run.ID)
	if got.Status != "failed" || got.Error == nil || *got.Error != "session vanished" {
		t.Fatalf("run %+v", got)
	}
	if e.get(t, tk.ID).Status != store.StatusNeedsYou {
		t.Fatalf("task %s", e.get(t, tk.ID).Status)
	}
	cs, _ := store.ListComments(e.db, tk.ID)
	if len(cs) != 1 || cs[0].Author != store.AuthorSystem {
		t.Fatalf("comments %+v", cs)
	}
}

func TestReapInteractive(t *testing.T) {
	e := setup(t)
	tk := e.task(t, "t", 0)
	sess := "sess-manual"
	run, err := store.CreateRun(e.db, store.Run{TaskID: tk.ID, Kind: "interactive", Mode: "fresh", Status: "running", SessionID: &sess})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateTaskStatus(e.db, tk.ID, store.StatusRunning, store.ActorSystem); err != nil {
		t.Fatal(err)
	}

	os.WriteFile(filepath.Join(e.fake, "agents.json"), []byte(`[{"sessionId":"sess-manual","pid":42}]`), 0o644)
	e.now = time.Now().Add(time.Minute)
	e.d.reap(t.Context())
	if got, _ := store.GetRun(e.db, run.ID); got.Status != "running" {
		t.Fatal("reaped a live session")
	}

	os.WriteFile(filepath.Join(e.fake, "agents.json"), []byte(`[]`), 0o644)
	e.now = time.Now().Add(5 * time.Second) // younger than the grace period
	e.d.reap(t.Context())
	if got, _ := store.GetRun(e.db, run.ID); got.Status != "running" {
		t.Fatal("reaped inside the grace period")
	}

	e.now = time.Now().Add(time.Minute)
	e.d.reap(t.Context())
	got, _ := store.GetRun(e.db, run.ID)
	if got.Status != "finished" || got.Error != nil || got.EndedAt == nil {
		t.Fatalf("run %+v", got)
	}
	if e.get(t, tk.ID).Status != store.StatusNeedsYou {
		t.Fatalf("task %s", e.get(t, tk.ID).Status)
	}
}

func TestReapAgy(t *testing.T) {
	e := setup(t)
	runnerName := "agy"
	tk := e.task(t, "t", 0)
	tk.Runner = &runnerName
	if err := store.UpdateTask(e.db, tk); err != nil {
		t.Fatal(err)
	}

	pid := "9999999" // non-existent PID
	run, err := store.CreateRun(e.db, store.Run{
		TaskID: tk.ID, Kind: "auto", Mode: "fresh", Status: "running", BgID: &pid,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateTaskStatus(e.db, tk.ID, store.StatusRunning, store.ActorSystem); err != nil {
		t.Fatal(err)
	}

	logPath := runner.BgLogPath(e.d.Home, tk.Key)
	os.MkdirAll(filepath.Dir(logPath), 0o755)
	os.WriteFile(logPath, []byte("<conveyor>\nstatus: done\nsummary: completed agy run\nstate: clean\nnext: none\n</conveyor>"), 0o644)

	e.now = time.Now().Add(time.Minute)
	e.d.reap(t.Context())

	got, err := store.GetRun(e.db, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "finished" {
		t.Fatalf("expected run finished, got %s", got.Status)
	}
	if got.Summary == nil || *got.Summary != "completed agy run" {
		t.Fatalf("unexpected summary: %v", got.Summary)
	}
	if e.get(t, tk.ID).Status != store.StatusReview {
		t.Fatalf("expected task status review, got %s", e.get(t, tk.ID).Status)
	}
}

func TestReapVanishedWithQueuedCommentRequeues(t *testing.T) {
	e := setup(t)
	tk := e.task(t, "t", 0)
	e.tick(t)
	if _, err := store.AddComment(e.db, store.Comment{TaskID: tk.ID, Author: store.AuthorHuman, Body: "while running"}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(e.fake, "agents.json"), []byte(`[]`), 0o644)
	e.now = time.Now().Add(time.Minute)
	e.tick(t) // reaps, requeues, and relaunches with the queued comment
	if left, _ := store.UnconsumedComments(e.db, tk.ID); len(left) != 0 {
		t.Fatalf("%d comments still queued; task %s", len(left), e.get(t, tk.ID).Status)
	}
}

func TestAdoptSessionWithoutHooks(t *testing.T) {
	e := setup(t)
	tk := e.task(t, "t", 0)
	e.tick(t) // launches, bg abcd0001, hooks never fire
	os.WriteFile(filepath.Join(e.fake, "agents.json"), []byte(`[{"id":"abcd0001","sessionId":"sess-9","status":"busy"}]`), 0o644)
	e.tick(t)
	run, _ := store.LatestRun(e.db, tk.ID)
	if run.Status != "running" || run.SessionID == nil || *run.SessionID != "sess-9" {
		t.Fatalf("run %+v", run)
	}
}

func TestReapLaunchingNoBg(t *testing.T) {
	e := setup(t)
	tk := e.task(t, "t", 0)
	// Simulate a crash between CreateRun and saving the bg id.
	run, err := store.CreateRun(e.db, store.Run{TaskID: tk.ID, Kind: "auto", Mode: "fresh", Status: "launching"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateTaskStatus(e.db, tk.ID, store.StatusRunning, store.ActorSystem); err != nil {
		t.Fatal(err)
	}
	e.now = time.Now().Add(5 * time.Second) // inside the grace period
	e.d.reap(t.Context())
	if got, _ := store.GetRun(e.db, run.ID); got.Status != "launching" {
		t.Fatal("reaped inside the grace period")
	}

	e.now = time.Now().Add(time.Minute)
	if err := e.d.reap(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetRun(e.db, run.ID)
	if got.Status != "failed" || got.Error == nil || *got.Error != "launch interrupted" {
		t.Fatalf("run %+v", got)
	}
	if e.get(t, tk.ID).Status != store.StatusNeedsYou {
		t.Fatalf("task %s", e.get(t, tk.ID).Status)
	}
	if cs, _ := store.ListComments(e.db, tk.ID); len(cs) != 1 || cs[0].Author != store.AuthorSystem {
		t.Fatalf("comments %+v", cs)
	}
}

func TestLockExclusive(t *testing.T) {
	home := t.TempDir()
	rel, err := Lock(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(home); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock: %v", err)
	}
	rel()
	rel2, err := Lock(home)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	rel2()
}

func TestPRMergeRemovesSessions(t *testing.T) {
	e := setup(t)
	ghDir := t.TempDir()
	gh := "#!/bin/sh\necho '[{\"url\":\"https://x/pr/1\",\"state\":\"MERGED\",\"number\":1,\"reviewDecision\":\"\",\"updatedAt\":\"2026-01-01T00:00:00Z\"}]'\n"
	if err := os.WriteFile(filepath.Join(ghDir, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	tk, err := store.CreateTask(e.db, store.Task{Title: "Merged", Status: store.StatusReview},
		[]store.TaskRepo{{RepoID: e.repo.ID, IsPrimary: true, Branch: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	bg := "abcd0001"
	if _, err := store.CreateRun(e.db, store.Run{TaskID: tk.ID, Kind: "auto", Mode: "fresh", Status: "finished", BgID: &bg}); err != nil {
		t.Fatal(err)
	}
	e.d.pollPRs(t.Context())
	if got := e.get(t, tk.ID); got.Status != store.StatusDone {
		t.Fatalf("status %s", got.Status)
	}
	b, _ := os.ReadFile(filepath.Join(e.fake, "log"))
	if !strings.Contains(string(b), "|rm\x1fabcd0001\x1f\n") {
		t.Fatalf("no rm call in log: %q", b)
	}
}

func TestModelPrecedence(t *testing.T) {
	e := setup(t)
	t.Setenv("HOME", t.TempDir())
	store.SetSetting(e.db, "default_model", "sonnet")
	dir := filepath.Join(e.repo.Path, ".claude", "agents")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("---\nname: a\nmodel: opus\n---\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("---\nname: b\n---\n"), 0o644)
	str := func(s string) *string { return &s }
	for _, c := range []struct {
		model, agent *string
		want         string
	}{
		{nil, nil, "sonnet"}, {str("haiku"), str("a"), "haiku"}, {nil, str("a"), ""}, {nil, str("b"), "sonnet"},
	} {
		tk := e.task(t, "t", 0)
		tk.Model, tk.Agent = c.model, c.agent
		if got := e.d.model(tk); got != c.want {
			t.Errorf("%v %v: got %q want %q", c.model, c.agent, got, c.want)
		}
	}
}
