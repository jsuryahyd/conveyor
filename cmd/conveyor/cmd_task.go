package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/JayaSuryaOolio/conveyor/internal/brief"
	"github.com/JayaSuryaOolio/conveyor/internal/core"
	"github.com/JayaSuryaOolio/conveyor/internal/gh"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

func init() {
	register("task", runTask, "manage tasks (new|list|show|comment|move|set|repo|link|unlink|parent|sessions|stop|cancel|done|sync|brief|log|attach|open|label|graph)")
}

const taskUsage = "usage: conveyor task new|list|show|comment|move|set|link|unlink|parent|sessions|stop|cancel|done|sync|brief|log|attach|open|label|graph ..."

func runTask(home string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", taskUsage)
	}
	cmd, rest := args[0], args[1:]
	return withService(home, func(s *core.Service) error {
		switch cmd {
		case "new":
			return taskNew(s, rest)
		case "list":
			return taskList(s, rest)
		case "show":
			return taskShow(s, rest)
		case "comment":
			return taskComment(s, rest)
		case "move":
			stop := len(rest) == 3 && rest[2] == "--stop"
			if len(rest) != 2 && !stop {
				return fmt.Errorf("usage: conveyor task move KEY <status> [--stop]")
			}
			to := store.Status(rest[1])
			if !to.Valid() {
				return fmt.Errorf("unknown status %q", rest[1])
			}
			return withStopHint(s.Move(rest[0], to, stop))
		case "set":
			return taskSet(s, rest)
		case "link", "unlink":
			if len(rest) != 3 {
				return fmt.Errorf("usage: conveyor task %s KEY blocks|relates OTHER", cmd)
			}
			if cmd == "link" {
				return s.Link(rest[0], rest[1], rest[2])
			}
			return s.Unlink(rest[0], rest[1], rest[2])
		case "label":
			if len(rest) != 3 || (rest[1] != "add" && rest[1] != "rm" && rest[1] != "set") {
				return fmt.Errorf("usage: conveyor task label KEY add|rm NAME | set <prefix>:<value> (set replaces the labels with that prefix)")
			}
			switch rest[1] {
			case "add":
				return s.AddLabel(rest[0], rest[2])
			case "set":
				return s.SetLabel(rest[0], rest[2])
			}
			return s.RemoveLabel(rest[0], rest[2])
		case "graph":
			return taskGraph(s, rest)
		case "repo":
			if len(rest) != 3 || (rest[1] != "add" && rest[1] != "rm") {
				return fmt.Errorf("usage: conveyor task repo KEY add|rm NAME")
			}
			if rest[1] == "add" {
				return s.AddTaskRepo(rest[0], rest[2])
			}
			return s.RemoveTaskRepo(rest[0], rest[2])
		case "parent":
			if len(rest) != 2 {
				return fmt.Errorf("usage: conveyor task parent KEY PARENT|--none")
			}
			if rest[1] == "--none" {
				rest[1] = ""
			}
			return s.SetParent(rest[0], rest[1])
		case "log":
			return taskLog(s, rest)
		case "attach":
			return taskAttach(s, rest)
		case "open":
			return taskOpen(s, rest)
		case "brief":
			if len(rest) == 2 && rest[1] == "--preview" {
				return taskBriefPreview(s, rest[0])
			}
			if len(rest) != 1 {
				return fmt.Errorf("usage: conveyor task brief KEY [--preview]")
			}
			return taskBrief(s, rest[0])
		case "sync":
			if len(rest) != 1 {
				return fmt.Errorf("usage: conveyor task sync KEY")
			}
			return taskSync(s, rest[0])
		case "sessions":
			if len(rest) < 1 || len(rest) > 2 || len(rest) == 2 && rest[1] != "--json" {
				return fmt.Errorf("usage: conveyor task sessions KEY [--json]")
			}
			ids, err := s.SessionIDs(rest[0])
			if err != nil {
				return err
			}
			if len(rest) == 2 {
				return printJSON(ids)
			}
			for _, id := range ids {
				fmt.Fprintln(out, id)
			}
			return nil
		case "cancel", "done", "stop":
			stop := cmd == "done" && len(rest) == 2 && rest[1] == "--stop"
			if len(rest) != 1 && !stop {
				return fmt.Errorf("usage: conveyor task %s KEY", cmd)
			}
			if cmd == "done" {
				return withStopHint(s.MarkDone(rest[0], stop))
			}
			if cmd == "stop" {
				return s.Stop(rest[0])
			}
			return s.Cancel(rest[0])
		}
		return fmt.Errorf("unknown task command %q\n%s", cmd, taskUsage)
	})
}

func taskNew(s *core.Service, args []string) error {
	fs := flag.NewFlagSet("task new", flag.ContinueOnError)
	repos := fs.String("repo", "", "repo name(s), comma separated (sub-tasks default to the parent's)")
	primary := fs.String("primary", "", "primary repo (default: first)")
	desc := fs.String("desc", "", "description")
	descFile := fs.String("desc-file", "", "read description from file")
	todo := fs.Bool("todo", false, "start in todo instead of backlog")
	model := fs.String("model", "", "model override: a tier alias or id (see `conveyor models`); a wrong one fails the run and the task goes to needs_you")
	agent := fs.String("agent", "", "Claude subagent name (see conveyor agent list; sub-tasks default to the parent's)")
	runner := fs.String("runner", "", "runner override (claude, agy; default: from conveyor settings)")
	parent := fs.String("parent", "", "parent task key (the new task becomes its sub-task, e.g. CV-1A)")
	prio := fs.Int("priority", 0, "priority (higher first)")
	epic := fs.Bool("epic", false, "make the task an epic root (labels epic and epic:<name>)")
	epicName := fs.String("epic-name", "", "epic name, unique among open epics (default: the lower-cased key)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: conveyor task new <title> --repo a[,b] [flags]")
	}
	d := *desc
	if *descFile != "" {
		b, err := os.ReadFile(*descFile)
		if err != nil {
			return fmt.Errorf("read desc file: %w", err)
		}
		d = string(b)
	}
	if *parent != "" && strings.TrimSpace(d) == "" {
		return fmt.Errorf("a sub-task needs --desc or --desc-file: its agent sees only the title and description")
	}
	runnerSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "runner" {
			runnerSet = true
		}
	})
	t, err := s.CreateTask(core.NewTaskInput{
		Title: pos[0], Description: d, Repos: splitCSV(*repos), Primary: *primary, Start: *todo,
		Model: *model, Agent: *agent, Runner: *runner, RunnerSet: runnerSet, ParentKey: *parent, Priority: *prio, CreatedBy: createdBy(*parent),
		Epic: *epic || *epicName != "", EpicName: *epicName,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s %s (%s)\n", t.Key, t.Title, t.Status)
	return nil
}

func taskList(s *core.Service, args []string) error {
	fs := flag.NewFlagSet("task list", flag.ContinueOnError)
	status := fs.String("status", "", "status filter, comma separated")
	asJSON := fs.Bool("json", false, "JSON output")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	var sts []store.Status
	for _, v := range splitCSV(*status) {
		st := store.Status(v)
		if !st.Valid() {
			return fmt.Errorf("unknown status %q", v)
		}
		sts = append(sts, st)
	}
	tasks, err := store.ListTasks(s.DB, sts...)
	if err != nil {
		return err
	}
	if *asJSON {
		if tasks == nil {
			tasks = []store.Task{}
		}
		return printJSON(tasks)
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tSTATUS\tPRI\tREPOS\tTITLE")
	for _, t := range tasks {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", t.Key, t.Status, t.Priority, repoNames(t), t.Title)
	}
	return tw.Flush()
}

func repoNames(t store.Task) string {
	var n []string
	for _, r := range t.Repos {
		n = append(n, r.RepoName)
	}
	return strings.Join(n, ",")
}

func taskShow(s *core.Service, args []string) error {
	fs := flag.NewFlagSet("task show", flag.ContinueOnError)
	thread := fs.Bool("thread", false, "include comments")
	runs := fs.Bool("runs", false, "include runs")
	asJSON := fs.Bool("json", false, "JSON output")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: conveyor task show KEY [--thread] [--runs] [--json]")
	}
	t, err := store.GetTaskByKey(s.DB, pos[0])
	if err != nil {
		return err
	}
	var comments []store.Comment
	var rs []store.Run
	rel, err := store.ListRelations(s.DB, t)
	if err != nil {
		return err
	}
	if *thread {
		if comments, err = store.ListComments(s.DB, t.ID); err != nil {
			return err
		}
	}
	if *runs {
		if rs, err = store.ListRuns(s.DB, t.ID); err != nil {
			return err
		}
	}
	if *asJSON {
		return printJSON(struct {
			store.Task
			Comments  []store.Comment `json:"comments,omitempty"`
			Runs      []store.Run     `json:"runs,omitempty"`
			Relations store.Relations `json:"relations"`
		}{t, comments, rs, rel})
	}
	fmt.Fprintf(out, "%s  %s\nstatus: %s  priority: %d\n", t.Key, t.Title, t.Status, t.Priority)
	for _, r := range t.Repos {
		p := ""
		if r.IsPrimary {
			p = " (primary)"
		}
		fmt.Fprintf(out, "repo: %s%s  branch %s  worktree %s  pr %s %s\n", r.RepoName, p, r.Branch, deref(r.WorktreePath), deref(r.PRURL), deref(r.PRState))
	}
	if rel.Parent != nil {
		fmt.Fprintf(out, "parent: %s  %s (%s)\n", rel.Parent.Key, rel.Parent.Title, rel.Parent.Status)
	}
	if t.Runner != nil && *t.Runner != "" {
		fmt.Fprintf(out, "runner: %s\n", *t.Runner)
	}
	if len(t.Labels) > 0 {
		fmt.Fprintf(out, "labels: %s\n", strings.Join(t.Labels, ", "))
	}
	for _, c := range rel.Children {
		fmt.Fprintf(out, "child: %s  %s (%s)\n", c.Key, c.Title, c.Status)
	}
	for _, l := range rel.Links {
		fmt.Fprintf(out, "%s: %s  %s (%s)\n", l.Label, l.Task.Key, l.Task.Title, l.Task.Status)
	}
	if t.Description != "" {
		fmt.Fprintf(out, "\n%s\n", t.Description)
	}
	if *thread {
		fmt.Fprintln(out, "\nthread:")
		for _, c := range comments {
			fmt.Fprintf(out, "[%s] %s: %s\n", c.CreatedAt, c.Author, c.Body)
		}
	}
	if *runs {
		fmt.Fprintln(out, "\nruns:")
		for _, r := range rs {
			fmt.Fprintf(out, "#%d %s/%s %s peak=%d calls=%d denials=%d rereads=%d dupbash=%d big=%d %s\n", r.ID, r.Kind, r.Mode, r.Status,
				r.CtxPeak, r.APICalls, r.HookDenials, r.Rereads, r.DupBash, r.BigResults, deref(r.Summary))
		}
	}
	return nil
}

// taskComment posts a human comment. Once a PR of the task is merged it starts
// a follow-up sub-task, unless --ask (requeue to answer) or --note (just comment).
func taskComment(s *core.Service, args []string) error {
	fs := flag.NewFlagSet("task comment", flag.ContinueOnError)
	ask := fs.Bool("ask", false, "after a merged PR: requeue the task to answer, without changing code")
	note := fs.Bool("note", false, "after a merged PR: only add the comment")
	args, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(args) != 2 || *ask && *note {
		return fmt.Errorf("usage: conveyor task comment KEY <text|-> [--ask|--note]")
	}
	after := core.FollowUp
	if *ask {
		after = core.Ask
	} else if *note {
		after = core.Note
	}
	body := args[1]
	if body == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		body = strings.TrimSpace(string(b))
	}
	if body == "" {
		return fmt.Errorf("comment is empty")
	}
	_, err = s.AddComment(args[0], store.AuthorHuman, body, after)
	return err
}

// taskSet changes the branch and/or PR URL of one of the task's repos.
// Omitted flags keep the current value; --pr "" clears the PR.
func taskSet(s *core.Service, args []string) error {
	fs := flag.NewFlagSet("task set", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo name (default: the only repo)")
	branch := fs.String("branch", "", "worktree branch (only before the worktree exists)")
	pr := fs.String("pr", "", "PR URL (empty clears it)")
	model := fs.String("model", "", "model override (empty clears it); see `conveyor models`")
	agent := fs.String("agent", "", "agent override (empty clears it); see `conveyor agent list`")
	runner := fs.String("runner", "", "runner override (claude, agy; empty resets to default)")
	desc := fs.String("desc", "", "description; once a run saw the old one, a system comment quotes it for the next run")
	descFile := fs.String("desc-file", "", "read the description from a file")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: conveyor task set KEY [--repo r] [--branch b] [--pr url] [--model m] [--agent a] [--runner r] [--desc d | --desc-file f]")
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if *descFile != "" {
		b, err := os.ReadFile(*descFile)
		if err != nil {
			return err
		}
		*desc, given["desc"] = string(b), true
	}
	if given["desc"] {
		if err := s.SetTaskDescription(pos[0], *desc); err != nil {
			return err
		}
	}
	if given["model"] {
		if err := s.SetTaskModel(pos[0], *model); err != nil {
			return err
		}
	}
	if given["agent"] {
		if err := s.SetTaskAgent(pos[0], *agent); err != nil {
			return err
		}
	}
	if given["runner"] {
		if err := s.SetTaskRunner(pos[0], *runner); err != nil {
			return err
		}
	}
	if !given["branch"] && !given["pr"] {
		if given["model"] || given["agent"] || given["desc"] || given["runner"] {
			return nil
		}
		return fmt.Errorf("nothing to set: pass --branch, --pr, --model, --agent, --runner and/or --desc")
	}
	t, err := store.GetTaskByKey(s.DB, pos[0])
	if err != nil {
		return err
	}
	name := *repo
	if name == "" {
		if len(t.Repos) != 1 {
			return fmt.Errorf("task has %d repos: pass --repo", len(t.Repos))
		}
		name = t.Repos[0].RepoName
	}
	var cur store.TaskRepo
	found := false
	for _, r := range t.Repos {
		if r.RepoName == name {
			cur, found = r, true
		}
	}
	if !found {
		return fmt.Errorf("%s is not attached to %s: add it with `conveyor task repo %s add %s`", name, t.Key, t.Key, name)
	}
	if !given["branch"] {
		*branch = cur.Branch
	}
	if !given["pr"] {
		*pr = deref(cur.PRURL)
	}
	return s.SetTaskRepo(t.Key, name, *branch, *pr)
}

// taskLog prints the latest run's session chat; --follow keeps polling until the run ends.
func taskLog(s *core.Service, args []string) error {
	fs := flag.NewFlagSet("task log", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "keep printing until the run ends")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: conveyor task log KEY [--follow]")
	}
	t, err := store.GetTaskByKey(s.DB, pos[0])
	if err != nil {
		return err
	}
	run, err := store.LatestRun(s.DB, t.ID)
	if err != nil {
		return err
	}
	var after int64
	for {
		l, err := s.RunLog(run.ID, after)
		if err != nil {
			return err
		}
		after = l.Next
		for _, e := range l.Entries {
			switch e.Kind {
			case "user":
				fmt.Fprintf(out, "> %s\n", e.Text)
			case "tool":
				fmt.Fprintf(out, "  %s\n", e.Text)
			default:
				fmt.Fprintln(out, e.Text)
			}
		}
		if !*follow {
			return nil
		}
		if run, err = store.GetRun(s.DB, run.ID); err != nil {
			return err
		}
		if run.Status != "launching" && run.Status != "running" && len(l.Entries) == 0 {
			return nil
		}
		time.Sleep(time.Second)
	}
}

// taskSync refreshes PR state from gh and prints it.
func taskSync(s *core.Service, key string) error {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return err
	}
	if err := gh.SyncTask(context.Background(), s.DB, t); err != nil {
		return err
	}
	if t, err = store.GetTaskByKey(s.DB, key); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s  status: %s\n", t.Key, t.Status)
	for _, r := range t.Repos {
		fmt.Fprintf(out, "%s  %s  %s\n", r.RepoName, deref(r.PRState), deref(r.PRURL))
	}
	return nil
}

// taskBrief renders the task's brief and prints its content.
func taskBrief(s *core.Service, key string) error {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return err
	}
	path, err := brief.Render(s.DB, s.Home, t)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = out.Write(b)
	return err
}

// taskBriefPreview prints the brief and fresh prompt a launch would use, without writing BRIEF.md.
func taskBriefPreview(s *core.Service, key string) error {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return err
	}
	text, prompt, err := brief.Preview(s.DB, s.Home, t)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s\n--- fresh prompt ---\n%s\n", text, prompt)
	return nil
}

// createdBy attributes a CLI-made task: $CONVEYOR_CREATED_BY (set for automations), else the parent's agent, else a human.
func createdBy(parent string) string {
	if v := os.Getenv("CONVEYOR_CREATED_BY"); v != "" {
		return v
	}
	if parent != "" {
		return "agent:" + parent
	}
	return "human"
}

// withStopHint tells the CLI user how to confirm a move that ends running runs.
func withStopHint(err error) error {
	var a *core.ActiveRunsError
	if errors.As(err, &a) {
		return fmt.Errorf("%w; re-run with --stop to confirm", err)
	}
	return err
}

func taskGraph(s *core.Service, args []string) error {
	fs := flag.NewFlagSet("task graph", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "JSON output")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		return fmt.Errorf("usage: conveyor task graph KEY [--json]")
	}
	g, err := s.TaskGraph(pos[0])
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(g)
	}
	for _, n := range g.Nodes {
		fmt.Fprintf(out, "%s  %-9s %s", n.Key, n.Status, n.Title)
		if n.ParentKey != "" {
			fmt.Fprintf(out, "  (child of %s)", n.ParentKey)
		}
		fmt.Fprintln(out)
	}
	for _, l := range g.Links {
		fmt.Fprintf(out, "%s %s %s\n", l.From, l.Kind, l.To)
	}
	return nil
}
