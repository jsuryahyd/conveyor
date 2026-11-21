package hook

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayaSuryaOolio/conveyor/internal/core"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

type env struct {
	db   *sql.DB
	home string
	task store.Task
}

func setup(t *testing.T, st store.Status) env {
	t.Helper()
	home := t.TempDir()
	db, err := store.Open(core.DBPath(home))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r, err := store.AddRepo(db, store.Repo{Name: "a", Path: "/x/a", DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	tk, err := store.CreateTask(db, store.Task{Title: "t", Status: st}, []store.TaskRepo{{RepoID: r.ID, IsPrimary: true, Branch: "t"}})
	if err != nil {
		t.Fatal(err)
	}
	return env{db, home, tk}
}

func (e env) cwd() string { return filepath.Join(core.WorkspacesDir(e.home), e.task.Key, "a") }

func (e env) fire(t *testing.T, event string, m map[string]any) error {
	t.Helper()
	if _, ok := m["cwd"]; !ok {
		m["cwd"] = e.cwd()
	}
	_, err := e.fireOut(t, event, m)
	return err
}

func (e env) fireOut(t *testing.T, event string, m map[string]any) (string, error) {
	t.Helper()
	if _, ok := m["cwd"]; !ok {
		m["cwd"] = e.cwd()
	}
	b, _ := json.Marshal(m)
	return Handle(e.db, e.home, event, b)
}

func (e env) run(t *testing.T, kind, status, session, prompt string) store.Run {
	t.Helper()
	var sp *string
	if session != "" {
		sp = &session
	}
	r, err := store.CreateRun(e.db, store.Run{TaskID: e.task.ID, Kind: kind, Mode: "fresh", Status: status, SessionID: sp, Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (e env) status(t *testing.T) store.Task {
	t.Helper()
	tk, err := store.GetTask(e.db, e.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

const doneMsg = "All good.\n<conveyor>\nstatus: done\nsummary: s\ncriteria:\n- [x] AC1 — ok\nlearning: l1\nlearning: l1\nspawn: child | do the thing\nspawn: nodesc\n</conveyor>"
const failMsg = "<conveyor>\nstatus: failed\nsummary: nope\n</conveyor>"

func TestPromptSetsSessionAndNeedsYouResumes(t *testing.T) {
	e := setup(t, store.StatusNeedsYou)
	r := e.run(t, "auto", "launching", "", "p")
	if err := e.fire(t, "UserPromptSubmit", map[string]any{"session_id": "S1"}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetRun(e.db, r.ID)
	if got.SessionID == nil || *got.SessionID != "S1" || got.Status != "running" {
		t.Fatalf("%+v", got)
	}
	if e.status(t).Status != store.StatusRunning {
		t.Fatal("needs_you should become running")
	}
}

func TestStopDoneToReview(t *testing.T) {
	e := setup(t, store.StatusRunning)
	r := e.run(t, "auto", "running", "S1", "do it")
	tp := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(tp, []byte(`{"type":"user","message":{"content":"do it"}}`+"\n"+
		`{"type":"assistant","message":{"id":"m","usage":{"input_tokens":5,"output_tokens":2,"cache_read_input_tokens":10,"cache_creation_input_tokens":1}}}`+"\n"+
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}},{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`+"\n"+
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"Bash hook error: no"}]}}`+"\n"), 0o644)
	if err := e.fire(t, "Stop", map[string]any{"session_id": "S1", "last_assistant_message": doneMsg, "transcript_path": tp}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetRun(e.db, r.ID)
	if got.Status != "finished" || got.EndedAt == nil || *got.BlockStatus != "done" || *got.FinalMessage != "All good." ||
		*got.Criteria != "- [x] AC1 — ok" || got.CtxPeak != 16 || got.APICalls != 1 || got.TokensOut != 2 ||
		got.DupBash != 1 || got.HookDenials != 1 || got.Rereads != 0 {
		t.Fatalf("%+v", got)
	}
	if e.status(t).Status != store.StatusReview {
		t.Fatal("want review")
	}
	cs, _ := store.ListComments(e.db, e.task.ID)
	if len(cs) != 1 || cs[0].Author != "agent" || cs[0].Body != "All good." {
		t.Fatalf("%+v", cs)
	}
	var n int
	var repo sql.NullInt64
	e.db.QueryRow(`SELECT COUNT(*), MAX(repo_id) FROM learnings WHERE status='proposed'`).Scan(&n, &repo)
	if n != 1 || !repo.Valid {
		t.Fatalf("learnings %d %v", n, repo)
	}
	evs, _ := store.ListEvents(e.db, e.task.ID)
	spawn := 0
	for _, ev := range evs {
		if ev.Kind == "spawn" && ev.Detail == "child | do the thing" {
			spawn++
		}
	}
	if spawn != 1 {
		t.Fatal("want spawn event")
	}
	all, _ := store.ListTasks(e.db, store.StatusBacklog)
	if len(all) != 1 || all[0].Title != "child" || all[0].ParentID == nil || *all[0].ParentID != e.task.ID ||
		len(all[0].Repos) != 1 || all[0].Repos[0].RepoName != "a" || !all[0].Repos[0].IsPrimary {
		t.Fatalf("spawned child: %+v", all)
	}
	if d := all[0].Description; d != "do the thing" {
		t.Fatalf("spawned child description: %q", d)
	}
	failed := false
	for _, ev := range evs {
		failed = failed || ev.Kind == "spawn_failed" && strings.Contains(ev.Detail, "nodesc")
	}
	if !failed {
		t.Fatal("want spawn_failed for the line without a description")
	}
}

func TestStopSpawnFromEpicMemberCarriesEpicLabel(t *testing.T) {
	e := setup(t, store.StatusRunning)
	e.run(t, "auto", "running", "S1", "do it")
	if err := store.AddLabel(e.db, e.task.ID, store.PrefixEpic+"os-4"); err != nil {
		t.Fatal(err)
	}
	if err := e.fire(t, "Stop", map[string]any{"session_id": "S1", "last_assistant_message": doneMsg}); err != nil {
		t.Fatal(err)
	}
	all, _ := store.ListTasks(e.db, store.StatusBacklog)
	if len(all) != 1 || store.EpicName(all[0]) != "os-4" || store.LabelValue(all[0], "epic_kind:") != "" {
		t.Fatalf("spawned child labels: %+v", all)
	}
}

func TestStopSpawnFailureStillTransitions(t *testing.T) {
	e := setup(t, store.StatusRunning)
	e.run(t, "auto", "running", "S1", "do it")
	// No repo link on the parent makes SpawnChildren fail ("needs exactly one primary repo").
	if _, err := e.db.Exec(`DELETE FROM task_repos WHERE task_id=?`, e.task.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.fire(t, "Stop", map[string]any{"session_id": "S1", "last_assistant_message": doneMsg}); err != nil {
		t.Fatal(err)
	}
	if e.status(t).Status != store.StatusReview {
		t.Fatal("want review despite spawn failure")
	}
	evs, _ := store.ListEvents(e.db, e.task.ID)
	for _, ev := range evs {
		if ev.Kind == "spawn_failed" {
			return
		}
	}
	t.Fatal("want spawn_failed event")
}

func TestStopNoBlockNeedsYou(t *testing.T) {
	e := setup(t, store.StatusRunning)
	e.run(t, "auto", "running", "S1", "")
	if err := e.fire(t, "Stop", map[string]any{"session_id": "S1", "last_assistant_message": "question?"}); err != nil {
		t.Fatal(err)
	}
	if e.status(t).Status != store.StatusNeedsYou {
		t.Fatal("want needs_you")
	}
}

func TestStopFailedThenMax(t *testing.T) {
	e := setup(t, store.StatusRunning)
	store.SetSetting(e.db, "max_consecutive_failures", "2")
	e.run(t, "auto", "running", "S1", "")
	e.fire(t, "Stop", map[string]any{"session_id": "S1", "last_assistant_message": failMsg})
	tk := e.status(t)
	if tk.Status != store.StatusTodo || tk.ConsecutiveFailures != 1 {
		t.Fatalf("%+v", tk)
	}
	store.UpdateTaskStatus(e.db, tk.ID, store.StatusRunning, store.ActorSystem)
	e.run(t, "auto", "running", "S2", "")
	e.fire(t, "Stop", map[string]any{"session_id": "S2", "last_assistant_message": failMsg})
	tk = e.status(t)
	if tk.Status != store.StatusFailed || tk.ConsecutiveFailures != 2 {
		t.Fatalf("%+v", tk)
	}
}

func TestStopWithPendingCommentsToTodo(t *testing.T) {
	e := setup(t, store.StatusRunning)
	e.run(t, "auto", "running", "S1", "")
	store.AddComment(e.db, store.Comment{TaskID: e.task.ID, Author: store.AuthorHuman, Body: "also"})
	e.fire(t, "Stop", map[string]any{"session_id": "S1", "last_assistant_message": doneMsg})
	if e.status(t).Status != store.StatusTodo {
		t.Fatal("want todo")
	}
}

func TestInteractiveStopLeavesTask(t *testing.T) {
	e := setup(t, store.StatusReview)
	// no run yet: an interactive run is created on the fly
	if err := e.fire(t, "Stop", map[string]any{"session_id": "I1", "last_assistant_message": doneMsg}); err != nil {
		t.Fatal(err)
	}
	if e.status(t).Status != store.StatusReview {
		t.Fatal("status must not change")
	}
	r, err := store.RunBySession(e.db, "I1")
	if err != nil || r.Kind != "interactive" || r.Status != "finished" || r.FinalMessage == nil {
		t.Fatalf("%+v %v", r, err)
	}
	if cs, _ := store.ListComments(e.db, e.task.ID); len(cs) != 0 {
		t.Fatal("no comment for interactive")
	}
}

func TestInteractiveStopReleasesRunningTask(t *testing.T) {
	for _, queued := range []bool{false, true} {
		e := setup(t, store.StatusRunning)
		e.run(t, "interactive", "running", "I1", "")
		if queued {
			store.AddComment(e.db, store.Comment{TaskID: e.task.ID, Author: store.AuthorHuman, Body: "hi"})
		}
		if err := e.fire(t, "Stop", map[string]any{"session_id": "I1", "last_assistant_message": "x"}); err != nil {
			t.Fatal(err)
		}
		want := store.StatusNeedsYou
		if queued {
			want = store.StatusTodo
		}
		if got := e.status(t).Status; got != want {
			t.Fatalf("queued=%v: status %s, want %s", queued, got, want)
		}
	}
}

func TestResumeUsesNewestRunOfSession(t *testing.T) {
	e := setup(t, store.StatusRunning)
	e.run(t, "auto", "finished", "S1", "first")
	r2 := e.run(t, "auto", "running", "S1", "second")
	e.fire(t, "Stop", map[string]any{"session_id": "S1", "last_assistant_message": "q"})
	got, _ := store.GetRun(e.db, r2.ID)
	if got.Status != "finished" {
		t.Fatalf("%+v", got)
	}
}

func TestNotification(t *testing.T) {
	e := setup(t, store.StatusRunning)
	e.fire(t, "Notification", map[string]any{"message": "needs input", "notification_type": "idle"})
	evs, _ := store.ListEvents(e.db, e.task.ID)
	if len(evs) == 0 || evs[len(evs)-1].Kind != "notification" {
		t.Fatalf("%+v", evs)
	}
}

func TestQuestionNotificationParksTask(t *testing.T) {
	e := setup(t, store.StatusRunning)
	e.run(t, "auto", "running", "S1", "p")
	e.fire(t, "Notification", map[string]any{"notification_type": "elicitation_dialog", "message": "q?"})
	if e.status(t).Status != store.StatusNeedsYou {
		t.Fatalf("%+v", e.status(t))
	}
	e.fire(t, "Stop", map[string]any{"session_id": "S1", "last_assistant_message": doneMsg})
	if e.status(t).Status != store.StatusReview {
		t.Fatalf("%+v", e.status(t))
	}
}

func TestOutsideWorkspacesIgnored(t *testing.T) {
	e := setup(t, store.StatusRunning)
	for _, cwd := range []string{"/somewhere/else", core.WorkspacesDir(e.home), ""} {
		if err := e.fire(t, "Stop", map[string]any{"cwd": cwd, "session_id": "S1", "last_assistant_message": doneMsg}); err != nil {
			t.Fatal(err)
		}
	}
	if rs, _ := store.ListRuns(e.db, e.task.ID); len(rs) != 0 {
		t.Fatal("no run expected")
	}
	if e.status(t).Status != store.StatusRunning {
		t.Fatal("unchanged")
	}
}

// A bg resume reported cwd /private/tmp and its Stop was dropped, leaving the run active and the queue stuck.
func TestStopWithStrayCwdFindsRunBySession(t *testing.T) {
	e := setup(t, store.StatusRunning)
	r := e.run(t, "auto", "running", "S1", "p")
	if err := e.fire(t, "Stop", map[string]any{"cwd": "/private/tmp", "session_id": "S1", "last_assistant_message": doneMsg}); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetRun(e.db, r.ID); got.Status != "finished" || e.status(t).Status != store.StatusReview {
		t.Fatalf("%+v %+v", got, e.status(t))
	}
}

func TestMalformedJSON(t *testing.T) {
	e := setup(t, store.StatusRunning)
	if _, err := Handle(e.db, e.home, "Stop", []byte("{nope")); err == nil {
		t.Fatal("want error")
	}
}

func TestStopWithPendingBackgroundJobStaysRunning(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	prompt := `{"type":"user","message":{"content":"do it"}}` + "\n"
	launch := `{"type":"assistant","timestamp":"` + now + `","message":{"content":[{"type":"tool_use","name":"Monitor","input":{}}]}}` + "\n"
	cases := []struct {
		name, transcript string
		want             store.Status
	}{
		{"pending", prompt + launch, store.StatusRunning},
		{"notified", prompt + launch + `{"type":"user","message":{"content":"<task-notification>done</task-notification>"}}` + "\n", store.StatusNeedsYou},
		{"api error after launch", prompt + launch + `{"type":"assistant","isApiErrorMessage":true,"message":{"content":[{"type":"text","text":"bad model"}]}}` + "\n", store.StatusNeedsYou},
		{"no launch", prompt, store.StatusNeedsYou},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t, store.StatusRunning)
			e.run(t, "auto", "running", "S1", "do it")
			tp := filepath.Join(t.TempDir(), "t.jsonl")
			os.WriteFile(tp, []byte(c.transcript), 0o644)
			if err := e.fire(t, "Stop", map[string]any{"session_id": "S1", "last_assistant_message": "waiting", "transcript_path": tp}); err != nil {
				t.Fatal(err)
			}
			if got := e.status(t).Status; got != c.want {
				t.Fatalf("got %s want %s", got, c.want)
			}
		})
	}
}

// A resume that Claude runs as a copy reports a new session id; it must land on the resume run, not a new interactive one.
func TestResumeAsCopyAdoptsLaunchingRun(t *testing.T) {
	e := setup(t, store.StatusRunning)
	e.run(t, "auto", "finished", "S1", "first")
	r2 := e.run(t, "auto", "launching", "S1", "second") // dispatcher pre-fills the resumed id
	e.fire(t, "UserPromptSubmit", map[string]any{"session_id": "S2"})
	e.fire(t, "Stop", map[string]any{"session_id": "S2", "last_assistant_message": doneMsg})
	runs, _ := store.ListRuns(e.db, e.task.ID)
	got, _ := store.GetRun(e.db, r2.ID)
	if len(runs) != 2 || got.Status != "finished" || *got.SessionID != "S2" || e.status(t).Status != store.StatusReview {
		t.Fatalf("%d runs, %+v", len(runs), got)
	}
}

const plainDone = "<conveyor>\nstatus: done\nsummary: charted\n</conveyor>"

func TestEpicGate(t *testing.T) {
	for _, gateOff := range []bool{false, true} {
		e := setup(t, store.StatusRunning)
		if err := store.MakeEpic(e.db, e.task.ID, "os"); err != nil {
			t.Fatal(err)
		}
		if gateOff {
			store.AddLabel(e.db, e.task.ID, store.LabelGateOff)
		}
		e.run(t, "auto", "running", "S1", "chart it")
		if err := e.fire(t, "Stop", map[string]any{"session_id": "S1", "last_assistant_message": plainDone}); err != nil {
			t.Fatal(err)
		}
		got := e.status(t)
		f, _ := store.ActiveFlow(e.db, e.task.ID)
		switch {
		case !gateOff && (got.Status != store.StatusNeedsYou || f != nil || store.EpicStage(got) != store.StageChart):
			t.Fatalf("gate on: status %s, flow %v, stage %s", got.Status, f, store.EpicStage(got))
		case gateOff && (got.Status != store.StatusReview || f == nil || store.EpicStage(got) != store.StageDecide):
			t.Fatalf("gate off: status %s, flow %v, stage %s", got.Status, f, store.EpicStage(got))
		}
	}
}

func TestPreToolUseDeniesMergeForEpicMembers(t *testing.T) {
	e := setup(t, store.StatusRunning)
	in := func(cmd string) []byte {
		b, _ := json.Marshal(map[string]any{"tool_name": "Bash", "cwd": e.cwd(), "tool_input": map[string]any{"command": cmd}})
		return b
	}
	member := func(cwd string) bool { return EpicMember(e.db, e.home, cwd) }
	none := func() string { return "" }
	for _, cmd := range []string{"gh pr merge 12 --squash", "gh pr -R o/r merge 12", "cd x && gh api repos/o/r/pulls/12/merge -X PUT"} {
		if got := PreToolUse(in(cmd), none, member); got != nil {
			t.Errorf("%q allowed outside an epic: %s", cmd, got)
		}
	}
	if err := store.MakeEpic(e.db, e.task.ID, "os"); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"gh pr merge 12 --squash", "gh pr -R o/r merge 12", "cd x && gh api repos/o/r/pulls/12/merge -X PUT"} {
		if got := PreToolUse(in(cmd), none, member); !strings.Contains(string(got), `"deny"`) {
			t.Errorf("%q not denied for an epic member: %s", cmd, got)
		}
	}
	for _, cmd := range []string{"gh pr view 12", "gh pr create --title merge", "echo gh pr merge"} {
		if got := PreToolUse(in(cmd), none, member); got != nil {
			t.Errorf("%q denied: %s", cmd, got)
		}
	}
}

func TestStopAgyReadsLogWhenMessageEmpty(t *testing.T) {
	e := setup(t, store.StatusRunning)
	sess := "sess-agy"
	bg := "12345"
	r, err := store.CreateRun(e.db, store.Run{TaskID: e.task.ID, Kind: "auto", Mode: "fresh", Status: "running", SessionID: &sess, BgID: &bg, Runner: "agy"})
	if err != nil {
		t.Fatal(err)
	}

	logDir := filepath.Join(e.home, "agy-logs")
	_ = os.MkdirAll(logDir, 0o755)
	logContent := "Work completed successfully.\n\n<conveyor>\nstatus: done\nsummary: done agy work\nstate: pass\nnext: none\n</conveyor>\n"
	if err := os.WriteFile(filepath.Join(logDir, e.task.Key+".log"), []byte(logContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := e.fire(t, "Stop", map[string]any{"session_id": sess, "last_assistant_message": ""}); err != nil {
		t.Fatal(err)
	}

	updated, err := store.GetRun(e.db, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "finished" {
		t.Errorf("status = %s, want finished", updated.Status)
	}
	if deref(updated.BlockStatus) != "done" {
		t.Errorf("block_status = %v, want done", deref(updated.BlockStatus))
	}
	if deref(updated.Summary) != "done agy work" {
		t.Errorf("summary = %v, want done agy work", deref(updated.Summary))
	}

	tk, _ := store.GetTask(e.db, e.task.ID)
	if tk.Status != store.StatusReview {
		t.Errorf("task status = %s, want review", tk.Status)
	}
}

func TestResolveRunAdoptsAgyAutoRun(t *testing.T) {
	e := setup(t, store.StatusRunning)
	oldSess := "stale-detected-sess"
	bg := "54321"
	r, err := store.CreateRun(e.db, store.Run{TaskID: e.task.ID, Kind: "auto", Mode: "fresh", Status: "running", SessionID: &oldSess, BgID: &bg, Runner: "agy"})
	if err != nil {
		t.Fatal(err)
	}

	realSess := "real-conv-id"
	logDir := filepath.Join(e.home, "agy-logs")
	_ = os.MkdirAll(logDir, 0o755)
	logContent := "<conveyor>\nstatus: done\nsummary: adopted\nstate: pass\nnext: none\n</conveyor>\n"
	_ = os.WriteFile(filepath.Join(logDir, e.task.Key+".log"), []byte(logContent), 0o644)

	if err := e.fire(t, "Stop", map[string]any{"conversationId": realSess, "last_assistant_message": ""}); err != nil {
		t.Fatal(err)
	}

	runs, err := store.ListRuns(e.db, e.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].ID != r.ID {
		t.Errorf("run id = %d, want %d", runs[0].ID, r.ID)
	}
	if deref(runs[0].SessionID) != realSess {
		t.Errorf("session id = %s, want %s", deref(runs[0].SessionID), realSess)
	}
}
