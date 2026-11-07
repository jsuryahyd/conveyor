package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/JayaSuryaOolio/conveyor/internal/core"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cvbin")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "conveyor")
	if b, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		panic(string(b))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func cv(t *testing.T, home string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"--home", home}, args...)...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("conveyor %v: %v\n%s", args, err, b)
	}
	return string(b)
}

func TestCLIFlow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir()) // keep the real ~/.claude/agents out of `agent list`
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "i"}} {
		if b, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	name := filepath.Base(repo)
	if out := cv(t, home, "repo", "add", repo); !strings.Contains(out, "default branch main") {
		t.Fatal(out)
	}
	cv(t, home, "repo", "setup", name, "direnv allow .")
	if out := cv(t, home, "repo", "setup", name); strings.TrimSpace(out) != "direnv allow ." {
		t.Fatal(out)
	}
	cv(t, home, "task", "new", "Do the thing", "--repo", name, "--todo")
	var tasks []struct {
		Key    string `json:"key"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(cv(t, home, "task", "list", "--json")), &tasks); err != nil || len(tasks) != 1 || tasks[0].Key != "CV-1" || tasks[0].Status != "todo" {
		t.Fatalf("list: %v %+v", err, tasks)
	}
	cv(t, home, "task", "comment", "CV-1", "hello")
	if out := cv(t, home, "task", "show", "CV-1", "--thread"); !strings.Contains(out, "human: hello") {
		t.Fatal(out)
	}
	cv(t, home, "config", "set", "max_parallel", "7")
	if out := cv(t, home, "config", "get", "max_parallel"); strings.TrimSpace(out) != "7" {
		t.Fatal(out)
	}
	cv(t, home, "task", "set", "CV-1", "--branch", "feat/x", "--pr", "https://gh/pr/1")
	if out := cv(t, home, "task", "show", "CV-1"); !strings.Contains(out, "branch feat/x") || !strings.Contains(out, "https://gh/pr/1") {
		t.Fatal(out)
	}
	cv(t, home, "task", "set", "CV-1", "--model", "opus")
	if out := cv(t, home, "task", "show", "CV-1", "--json"); !strings.Contains(out, `"model": "opus"`) {
		t.Fatal(out)
	}
	cv(t, home, "task", "set", "CV-1", "--pr", "")
	if out := cv(t, home, "task", "show", "CV-1"); !strings.Contains(out, "branch feat/x") || strings.Contains(out, "gh/pr") {
		t.Fatal(out)
	}
	if b, err := exec.Command(bin, "--home", home, "task", "new", "NoDesc", "--repo", name, "--parent", "CV-1").CombinedOutput(); err == nil {
		t.Fatalf("sub-task without --desc was created: %s", b)
	}
	if out := cv(t, home, "task", "new", "Sub", "--repo", name, "--parent", "CV-1", "--desc", "d"); !strings.HasPrefix(out, "CV-1A ") {
		t.Fatal(out)
	}
	cv(t, home, "task", "new", "Other", "--repo", name)
	cv(t, home, "task", "link", "CV-1A", "blocks", "CV-2")
	if out := cv(t, home, "task", "show", "CV-2"); !strings.Contains(out, "is blocked by: CV-1A") {
		t.Fatal(out)
	}
	if out := cv(t, home, "task", "show", "CV-1"); !strings.Contains(out, "child: CV-1A") {
		t.Fatal(out)
	}
	cv(t, home, "task", "unlink", "CV-1A", "blocks", "CV-2")
	if out := cv(t, home, "task", "show", "CV-2"); strings.Contains(out, "blocked") {
		t.Fatal(out)
	}
	cv(t, home, "agent", "add", "rev", "--desc", "Reviews", "--prompt", "Be strict", "--model", "opus", "--repo", name)
	if out := cv(t, home, "agent", "list", "--repo", name); !strings.Contains(out, "MODEL") || !strings.Contains(out, "opus") {
		t.Fatal(out)
	}
	cv(t, home, "agent", "model", "rev", "", "--repo", name)
	if out := cv(t, home, "agent", "list", "--repo", name); strings.Contains(out, "opus") {
		t.Fatal(out)
	}
	cv(t, home, "task", "cancel", "CV-1")
	if out := cv(t, home, "task", "list", "--status", "cancelled"); !strings.Contains(out, "CV-1") {
		t.Fatal(out)
	}
	// a non-repo path is rejected
	if b, err := exec.Command(bin, "--home", home, "repo", "add", t.TempDir()).CombinedOutput(); err == nil {
		t.Fatalf("expected failure: %s", b)
	}
}

func TestHookAlwaysExitsZero(t *testing.T) {
	home := t.TempDir()
	cmd := exec.Command(bin, "--home", home, "hook", "Stop")
	cmd.Stdin = strings.NewReader("{garbage")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v %s", err, out)
	}
	if b, err := os.ReadFile(filepath.Join(home, "logs", "hook.log")); err != nil || len(b) == 0 {
		t.Fatalf("log: %v %q", err, b)
	}
}

// hook.log gets a line per Conveyor-session event (so a missing Stop shows) and none for foreign sessions.
func TestHookLogScope(t *testing.T) {
	home := t.TempDir()
	hook := func(event, cwd string) {
		cmd := exec.Command(bin, "--home", home, "hook", event)
		cmd.Stdin = strings.NewReader(`{"session_id":"s1","cwd":"` + cwd + `"}`)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v %s", err, out)
		}
	}
	hook("Stop", t.TempDir())
	if _, err := os.Stat(filepath.Join(home, "logs", "hook.log")); !os.IsNotExist(err) {
		t.Fatalf("foreign session logged: %v", err)
	}
	hook("Stop", filepath.Join(home, "workspaces", "CV-9", "repo"))
	b, _ := os.ReadFile(filepath.Join(home, "logs", "hook.log"))
	if !strings.Contains(string(b), "CV-9 [Stop] session=s1") {
		t.Fatalf("log: %q", b)
	}
}

func TestAutomationCLI(t *testing.T) {
	home := t.TempDir()
	cv(t, home, "automation", "add", "--template", "pr-sweep")
	if out := cv(t, home, "automation", "list"); !strings.Contains(out, "pr-sweep") || !strings.Contains(out, "*/15 * * * *") {
		t.Fatal(out)
	}
	cv(t, home, "automation", "disable", "pr-sweep")
	if out := cv(t, home, "automation", "list"); !strings.Contains(out, "false") {
		t.Fatal(out)
	}
	if b, err := exec.Command(bin, "--home", home, "automation", "add", "bad", "--schedule", "nope", "--kind", "cli", "--arg", "{}").CombinedOutput(); err == nil {
		t.Fatalf("expected bad schedule to fail: %s", b)
	}
	if b, err := exec.Command(bin, "--home", home, "automation", "add", "bad", "--schedule", "* * * * *", "--kind", "cli", "--arg", "not json").CombinedOutput(); err == nil || !strings.Contains(string(b), "not valid JSON") {
		t.Fatalf("expected invalid --arg to fail: %s", b)
	}
	cv(t, home, "automation", "rm", "pr-sweep")
}

// Model names are not validated: agent list resolves aliases, shows the default for a blank, and keeps unknown names.
func TestCLIModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "")
	if out := strings.Join(strings.Fields(cv(t, home, "models")), " "); !strings.Contains(out, "claude-fable-5-1 fable fable") || !strings.Contains(out, "(source: builtin)") {
		t.Fatal(out)
	}
	cv(t, home, "config", "set", "default_model", "sonnet")
	cv(t, home, "agent", "add", "a", "--desc", "d", "--prompt", "p", "--model", "opus")
	cv(t, home, "agent", "add", "b", "--desc", "d", "--prompt", "p")
	cv(t, home, "agent", "add", "c", "--desc", "d", "--prompt", "p", "--model", "claude-whatever-1")
	out := cv(t, home, "agent", "list")
	for _, want := range []string{"opus -> claude-opus-5-5", "(default: sonnet)", "claude-whatever-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestReviewWebhookCLI(t *testing.T) {
	home, ghDir := t.TempDir(), t.TempDir()
	run := func() ([]byte, error) {
		return exec.Command(bin, "--home", home, "review-webhook").CombinedOutput()
	}
	t.Setenv("PATH", ghDir+":/usr/bin:/bin")

	// gh without the extension: a clear error, nothing started
	os.WriteFile(filepath.Join(ghDir, "gh"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	if b, err := run(); err == nil || !strings.Contains(string(b), "gh extension install cli/gh-webhook") {
		t.Fatalf("want missing-extension error, got %v: %s", err, b)
	}
	if _, err := exec.Command(bin, "--home", home, "review-webhook", "--status").CombinedOutput(); err == nil {
		t.Fatal("status ok with nothing running")
	}

	// gh with the extension (forward just sleeps): start, status, stop
	os.WriteFile(filepath.Join(ghDir, "gh"), []byte("#!/bin/sh\n[ \"$3\" = --help ] && exit 0\nsleep 30\n"), 0o755)
	if b, err := run(); err != nil {
		t.Fatalf("start: %v: %s", err, b)
	}
	for i := 0; i < 50 && runningPID(home, defaultApp) == 0; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if out := cv(t, home, "review-webhook", "--status"); !strings.Contains(out, "running") {
		t.Fatal(out)
	}
	cv(t, home, "review-webhook", "--stop")
	for i := 0; i < 50 && runningPID(home, defaultApp) != 0; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if runningPID(home, defaultApp) != 0 {
		t.Fatal("still running after --stop")
	}
	// a second instance under its own id gets its own pid file and log
	if out := cv(t, home, "review-webhook", "--app", "rw-v2"); !strings.Contains(out, "logs/app/rw-v2.log") {
		t.Fatal(out)
	}
	for i := 0; i < 50 && runningPID(home, "rw-v2") == 0; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if runningPID(home, defaultApp) != 0 || runningPID(home, "rw-v2") == 0 {
		t.Fatal("instances not independent")
	}
	if _, err := os.Stat(filepath.Join(home, "logs", "app", "rw-v2.log")); err != nil {
		t.Fatal(err)
	}
	cv(t, home, "review-webhook", "--app", "rw-v2", "--stop")
}

// cvFail runs conveyor expecting a non-zero exit and returns its output.
func cvFail(t *testing.T, home string, args ...string) string {
	t.Helper()
	b, err := exec.Command(bin, append([]string{"--home", home}, args...)...).CombinedOutput()
	if err == nil {
		t.Fatalf("conveyor %v: want failure\n%s", args, b)
	}
	return string(b)
}

func TestEpicCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "i"}} {
		if b, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	name := filepath.Base(repo)
	cv(t, home, "repo", "add", repo)

	desc := "## Acceptance criteria\n- [ ] AC1 first\n- [ ] AC2 second\n"
	cv(t, home, "task", "new", "Big thing", "--repo", name, "--epic", "--desc", desc)
	if out := cvFail(t, home, "task", "new", "Clash", "--repo", name, "--epic-name", "cv-1"); !strings.Contains(out, "taken") {
		t.Fatal(out) // default name is the lower-cased key
	}
	cv(t, home, "task", "new", "Build it", "--repo", name, "--parent", "CV-1", "--desc", "covers AC1", "--agent", "sse")
	cv(t, home, "task", "label", "CV-1A", "set", "epic_kind:build")

	// Checks: the child lacks epic:cv-1; AC2 is uncovered.
	out := cvFail(t, home, "epic", "check", "CV-1")
	for _, want := range []string{"CV-1A: missing label epic:cv-1", "AC2 is covered by no build task"} {
		if !strings.Contains(out, want) {
			t.Fatalf("check missing %q:\n%s", want, out)
		}
	}
	out = cvFail(t, home, "epic", "check", "CV-1", "--fix")
	if strings.Contains(out, "missing label") || !strings.Contains(out, "AC2") {
		t.Fatalf("--fix should repair labels only:\n%s", out)
	}
	cv(t, home, "task", "label", "CV-1A", "set", "epic_kind:research") // prefix swap keeps epic:cv-1
	if out := cv(t, home, "task", "show", "CV-1A"); !strings.Contains(out, "epic:cv-1") || !strings.Contains(out, "epic_kind:research") {
		t.Fatal(out)
	}

	// Status JSON, approve, restart.
	var st struct {
		Stage string
		Flow  *struct{ Attempt int }
		Tasks []struct{ Key string }
	}
	if err := json.Unmarshal([]byte(cv(t, home, "epic", "status", "CV-1", "--json")), &st); err != nil || st.Stage != "chart" || st.Flow != nil || len(st.Tasks) != 1 {
		t.Fatalf("status: %v %+v", err, st)
	}
	cv(t, home, "epic", "approve", "CV-1")
	cvFail(t, home, "epic", "approve", "CV-1") // only once
	if out := cv(t, home, "task", "show", "CV-1"); !strings.Contains(out, "epic_stage:decide") {
		t.Fatal(out)
	}
	cvFail(t, home, "epic", "restart", "CV-1") // reason required
	cv(t, home, "epic", "restart", "CV-1", "--reason", "wrong plan")
	if out := cv(t, home, "epic", "flows", "CV-1"); !strings.Contains(out, "flow 1  abandoned") || !strings.Contains(out, "flow 2  active") {
		t.Fatal(out)
	}
	if out := cv(t, home, "task", "show", "CV-1A"); !strings.Contains(out, "cancelled") {
		t.Fatal(out) // open sub-tasks are cancelled
	}
	if out := cv(t, home, "task", "graph", "CV-1A"); !strings.Contains(out, "CV-1  ") || !strings.Contains(out, "(child of CV-1)") {
		t.Fatal(out)
	}
}

// Hook, store and CLI actions of an epic member land in audit.jsonl; epic audit filters by flow and since.
func TestEpicAuditCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "i"}} {
		if b, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	name := filepath.Base(repo)
	cv(t, home, "repo", "add", repo)
	cv(t, home, "task", "new", "Big thing", "--repo", name, "--epic", "--desc", "## Acceptance criteria\n- [ ] AC1 x\n")
	cv(t, home, "task", "new", "Build it", "--repo", name, "--parent", "CV-1", "--desc", "AC1")
	cv(t, home, "task", "label", "CV-1A", "set", "epic:cv-1")
	cv(t, home, "epic", "approve", "CV-1") // flow 1

	cwd := filepath.Join(home, "workspaces", "CV-1A")
	os.MkdirAll(cwd, 0o755)
	hookEvent := func(event, payload string) string {
		c := exec.Command(bin, "--home", home, "hook", event)
		c.Stdin = strings.NewReader(payload)
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("hook %s: %v\n%s", event, err, b)
		}
		return string(b)
	}
	pay := func(extra string) string { return `{"session_id":"S1","cwd":"` + cwd + `"` + extra + `}` }
	hookEvent("UserPromptSubmit", pay(""))
	hookEvent("PreToolUse", pay(`,"tool_name":"Bash","tool_input":{"command":"git push origin HEAD"}`))
	if out := hookEvent("PreToolUse", pay(`,"tool_name":"Bash","tool_input":{"command":"gh pr merge 3"}`)); !strings.Contains(out, "deny") {
		t.Fatalf("merge not denied: %s", out)
	}
	hookEvent("PreToolUse", pay(`,"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which DB?","options":[{"label":"sqlite","description":"local"},{"label":"pg"}]}]}`))
	if th := cv(t, home, "task", "show", "CV-1A", "--thread"); !strings.Contains(th, "1. Which DB?") || !strings.Contains(th, "- sqlite: local") {
		t.Fatalf("AskUserQuestion not posted to the thread:\n%s", th)
	}
	hookEvent("Stop", pay(`,"last_assistant_message":"<conveyor>\nstatus: done\nsummary: s\n</conveyor>"`))
	cv(t, home, "epic", "log", "CV-1", "chose option B")
	cv(t, home, "epic", "restart", "CV-1", "--reason", "wrong plan") // flow 2

	events := func(args ...string) (ev []string) {
		for _, l := range strings.Split(strings.TrimSpace(cv(t, home, append([]string{"epic", "audit", "CV-1A", "--json"}, args...)...)), "\n") {
			var r map[string]any
			if err := json.Unmarshal([]byte(l), &r); err != nil || r["root"] != "CV-1" || r["epic"] != "cv-1" {
				t.Fatalf("bad record %q: %v", l, err)
			}
			ev = append(ev, r["event"].(string))
		}
		return
	}
	all := strings.Join(events(), " ")
	if n := strings.Count(all, "epic.run.start"); n != 1 {
		t.Errorf("epic.run.start x%d, want 1: %s", n, all)
	}
	for _, want := range []string{"epic.run.start", "epic.command", "epic.merge_denied", "epic.run.end", "epic.status_block", "epic.log", "epic.flow.end", "epic.flow.start"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %s in %s", want, all)
		}
	}
	if f1 := strings.Join(events("--flow", "1"), " "); !strings.Contains(f1, "epic.run.start") || strings.Count(f1, "epic.flow.start") != 1 {
		t.Errorf("flow 1: %s", f1)
	}
	if f2 := events("--flow", "2"); len(f2) != 1 || f2[0] != "epic.flow.start" {
		t.Errorf("flow 2: %v", f2)
	}
	if out := cv(t, home, "epic", "audit", "CV-1", "--since", "1h"); !strings.Contains(out, "chose option B") {
		t.Errorf("since 1h: %s", out)
	}
	time.Sleep(1100 * time.Millisecond)
	if out := strings.TrimSpace(cv(t, home, "epic", "audit", "CV-1", "--since", "1s")); out != "" {
		t.Errorf("since 1s should be empty: %s", out)
	}
}

// epic prs --commits and epic search read each PR branch's git log; epic sessions greps run transcripts.
func TestEpicBranchesAndSessionsCLI(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	t.Setenv("HOME", proj)
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if b, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	git("init", "-b", "main")
	git("commit", "--allow-empty", "-m", "init")
	git("checkout", "-b", "feat/x")
	for _, c := range []string{"widget", "gadget"} { // -G matches diff content, so each commit adds a file
		os.WriteFile(filepath.Join(repo, c+".txt"), []byte("the "+c+"\n"), 0o644)
		git("add", ".")
		git("commit", "-m", "add "+c)
	}
	git("checkout", "main")
	name := filepath.Base(repo)
	cv(t, home, "repo", "add", repo)
	cv(t, home, "task", "new", "Big thing", "--repo", name, "--epic", "--desc", "## Acceptance criteria\n- [ ] AC1 x\n")
	cv(t, home, "task", "new", "Build it", "--repo", name, "--parent", "CV-1", "--desc", "AC1")
	cv(t, home, "task", "new", "No PR", "--repo", name, "--parent", "CV-1", "--desc", "AC1") // skipped: no PR
	cv(t, home, "task", "set", "CV-1A", "--branch", "feat/x", "--pr", "https://gh/pr/1")

	// Worktree path and a run with a session are only set by the dispatcher, so write them directly.
	wt := "/work/CV-1A"
	db, err := store.Open(core.DBPath(home))
	if err != nil {
		t.Fatal(err)
	}
	tk, err := store.GetTaskByKey(db, "CV-1A")
	if err != nil {
		t.Fatal(err)
	}
	r := tk.Repos[0]
	r.WorktreePath = &wt
	if err := store.UpdateTaskRepo(db, r); err != nil {
		t.Fatal(err)
	}
	sid := "sess1"
	if _, err := store.CreateRun(db, store.Run{TaskID: tk.ID, Kind: "auto", Mode: "fresh", Status: "finished", SessionID: &sid}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	dir := filepath.Join(proj, ".claude", "projects", regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(wt, "-"))
	os.MkdirAll(dir, 0o755)
	tr := filepath.Join(dir, sid+".jsonl")
	os.WriteFile(tr, []byte(`{"type":"user","message":{"content":"hello"}}`+"\n"+`{"type":"assistant","text":"Fixed the FLUX capacitor"}`+"\n"), 0o644)

	out := cv(t, home, "epic", "prs", "CV-1", "--commits")
	if !strings.Contains(out, "CV-1A "+name+"/feat/x") || !strings.Contains(out, "add widget") || !strings.Contains(out, "add gadget") || strings.Contains(out, "init") || strings.Contains(out, "CV-1B") {
		t.Fatalf("prs --commits:\n%s", out)
	}
	if out := cv(t, home, "epic", "prs", "CV-1"); strings.Contains(out, "widget") {
		t.Fatalf("prs without --commits lists commits:\n%s", out)
	}

	out = cv(t, home, "epic", "search", "CV-1", "gadget")
	if !strings.Contains(out, "add gadget") || strings.Contains(out, "widget") {
		t.Fatalf("search gadget:\n%s", out)
	}
	if out := cv(t, home, "epic", "search", "CV-1", "nomatch"); strings.TrimSpace(out) != "" {
		t.Fatalf("search nomatch:\n%s", out)
	}

	out = cv(t, home, "epic", "sessions", "CV-1")
	if !strings.Contains(out, "CV-1A run 1 sess1 "+tr) {
		t.Fatalf("sessions:\n%s", out)
	}
	out = cv(t, home, "epic", "sessions", "CV-1", "--grep", "flux")
	if !strings.Contains(out, "sess1") || !strings.Contains(out, "    {\"type\":\"assistant\",\"text\":\"Fixed the FLUX capacitor\"}") {
		t.Fatalf("sessions --grep:\n%s", out)
	}
	if out := cv(t, home, "epic", "sessions", "CV-1", "--grep", "absent"); strings.TrimSpace(out) != "" {
		t.Fatalf("sessions --grep absent:\n%s", out)
	}
}

// AC1/AC4: attach a source, sync via a fake acli, detach.
func TestJiraCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "i"}} {
		if b, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	name := filepath.Base(repo)
	cv(t, home, "repo", "add", repo)
	cv(t, home, "task", "new", "Epic", "--repo", name, "--epic")

	acli := t.TempDir()
	script := "#!/bin/sh\ncase \"$5\" in \"key = OS-1 \"*) echo '[{\"key\":\"OS-1\"}]';; \"parent = OS-1\"*) echo '[{\"key\":\"OS-2\",\"fields\":{\"summary\":\"child\"}}]';; *) echo '[]';; esac\n"
	os.WriteFile(filepath.Join(acli, "acli"), []byte(script), 0o755)
	t.Setenv("PATH", acli+":"+os.Getenv("PATH"))

	if out := cv(t, home, "jira", "source", "CV-1"); !strings.Contains(out, "no jira source") {
		t.Fatal(out)
	}
	if out := cvFail(t, home, "jira", "source", "CV-1", "--key", "bad"); !strings.Contains(out, "invalid jira key") {
		t.Fatal(out)
	}
	if out := cvFail(t, home, "jira", "source", "CV-1", "--key", "OS-2"); !strings.Contains(out, "not assigned") {
		t.Fatal(out)
	}
	if out := cv(t, home, "jira", "source", "CV-1"); !strings.Contains(out, "no jira source") {
		t.Fatal(out)
	}
	cv(t, home, "jira", "source", "CV-1", "--key", "OS-1", "--repo", name, "--interval", "60")
	if out := cv(t, home, "jira", "sync", "CV-1"); !strings.Contains(out, "created CV-1A") {
		t.Fatal(out)
	}
	if out := cv(t, home, "jira", "source", "CV-1"); !strings.Contains(out, "key=OS-1") || !strings.Contains(out, "interval=60s") || strings.Contains(out, "last_sync=\n") {
		t.Fatal(out)
	}
	cv(t, home, "jira", "source", "CV-1", "--clear")
	if out := cv(t, home, "jira", "source", "CV-1"); !strings.Contains(out, "no jira source") {
		t.Fatal(out)
	}
}

// AC9: fake acli end to end through the binary: pull, idempotent re-pull, label->repo, write-back, epic completion.
func TestJiraEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	var names []string
	for i := 0; i < 2; i++ {
		repo := t.TempDir()
		for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "i"}} {
			if b, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, b)
			}
		}
		cv(t, home, "repo", "add", repo)
		names = append(names, filepath.Base(repo))
	}
	cv(t, home, "task", "new", "Epic", "--repo", names[0], "--epic")

	// search answers <dir>/<parent key>.json; every call is logged to <dir>/argv.
	dir := t.TempDir()
	script := `#!/bin/sh
echo "$*" >> ` + dir + `/argv
case "$5" in "key = "*) k=${5#key = }; echo "[{\"key\":\"${k%% *}\"}]"; exit 0;; esac
case "$3 $4" in "transition "*|"comment update") exit 0;; "comment create") echo '{"id":"c1"}'; exit 0;; esac
k=${5#parent = }; k=${k%% *}
cat ` + dir + `/$k.json 2>/dev/null || echo '[]'
`
	os.WriteFile(filepath.Join(dir, "acli"), []byte(script), 0o755)
	os.WriteFile(filepath.Join(dir, "OS-1.json"), []byte(`[
{"key":"OS-10","fields":{"summary":"Ten","labels":["repo:`+names[1]+`"]}},
{"key":"OS-11","fields":{"summary":"Eleven","labels":[]}}]`), 0o644)
	os.WriteFile(filepath.Join(dir, "OS-10.json"), []byte(`[{"key":"OS-100","fields":{"summary":"Sub","labels":[]}}]`), 0o644)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	argv := func() string { b, _ := os.ReadFile(filepath.Join(dir, "argv")); return string(b) }

	cv(t, home, "jira", "source", "CV-1", "--key", "OS-1", "--repo", names[0])
	out := cv(t, home, "jira", "sync", "CV-1")
	if strings.Count(out, "created ") != 3 {
		t.Fatalf("pull:\n%s", out)
	}
	// Keys: CV-1A=OS-10 (label repo), CV-1A1=OS-100 (sub-task), CV-1B=OS-11 (default repo).
	if s := cv(t, home, "task", "show", "CV-1A", "--json"); !strings.Contains(s, names[1]) || !strings.Contains(s, "OS-10") {
		t.Fatalf("label->repo:\n%s", s)
	}
	if s := cv(t, home, "task", "show", "CV-1B", "--json"); !strings.Contains(s, names[0]) {
		t.Fatalf("default repo:\n%s", s)
	}
	if out := cv(t, home, "jira", "sync", "CV-1"); strings.Contains(out, "created") {
		t.Fatalf("re-pull not idempotent:\n%s", out)
	}

	cv(t, home, "task", "move", "CV-1A", "todo")
	if !strings.Contains(argv(), "transition --key OS-10 --status To Do --yes") {
		t.Fatalf("no write-back:\n%s", argv())
	}
	if strings.Contains(argv(), "--key OS-1 ") && strings.Contains(argv(), "--status Done") {
		t.Fatal("epic completed early")
	}
	for _, k := range []string{"CV-1A", "CV-1A1", "CV-1B"} {
		cv(t, home, "task", "move", k, "cancelled") // done needs a run; cancelled also counts as finished
	}
	if !strings.Contains(argv(), "transition --key OS-1 --status Done --yes") {
		t.Fatalf("epic not completed:\n%s", argv())
	}
	if s := cv(t, home, "jira", "source", "CV-1"); !strings.Contains(s, "done=true") {
		t.Fatal(s)
	}
}

func TestTrustedWorkspacesCLI(t *testing.T) {
	home := t.TempDir()
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)

	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "i"}} {
		if b, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	name := filepath.Base(repo)

	// 1. conveyor repo add
	cv(t, home, "repo", "add", repo)

	settingsFile := filepath.Join(userHome, ".gemini", "antigravity-cli", "settings.json")
	b, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("settings file not found: %v", err)
	}
	var cfg struct {
		TrustedWorkspaces []string `json:"trustedWorkspaces"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	foundRepo := false
	foundWorkspaces := false
	foundGH := false
	for _, p := range cfg.TrustedWorkspaces {
		if p == repo {
			foundRepo = true
		}
		if p == filepath.Join(home, "workspaces") || p == filepath.Join(userHome, ".conveyor", "workspaces") {
			foundWorkspaces = true
		}
		if strings.HasSuffix(p, filepath.Join(".config", "gh")) {
			foundGH = true
		}
	}
	if !foundRepo || !foundWorkspaces || !foundGH {
		t.Fatalf("trustedWorkspaces missing expected paths after repo add: %v", cfg.TrustedWorkspaces)
	}

	// 2. conveyor task new and task set --runner agy
	cv(t, home, "task", "new", "Task1", "--repo", name)
	cv(t, home, "task", "set", "CV-1", "--runner", "agy")
	b, _ = os.ReadFile(settingsFile)
	_ = json.Unmarshal(b, &cfg)
	if len(cfg.TrustedWorkspaces) == 0 {
		t.Fatal("trustedWorkspaces empty after task set --runner agy")
	}

	// 3. conveyor config set default_runner agy
	cv(t, home, "config", "set", "default_runner", "agy")
	b, _ = os.ReadFile(settingsFile)
	_ = json.Unmarshal(b, &cfg)
	if len(cfg.TrustedWorkspaces) == 0 {
		t.Fatal("trustedWorkspaces empty after config set default_runner agy")
	}
}
