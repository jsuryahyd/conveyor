package core

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/JayaSuryaOolio/conveyor/internal/runner"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

// Service bundles the DB and home dir for domain operations.
type Service struct {
	DB   *sql.DB
	Home string
	// ProjectsDir overrides ~/.claude/projects (tests).
	ProjectsDir string
	// Bin is the conveyor CLI the hooks call; empty means this executable.
	// The desktop app must set it: its own executable is the GUI, not the CLI.
	Bin string
}

// NewTaskInput describes a task to create. Repos are registered repo names;
// Primary defaults to the first.
type NewTaskInput struct {
	Title       string
	Description string
	Repos       []string
	Primary     string
	Start       bool // true: status todo instead of backlog
	Model       string
	Agent       string
	AgentSet    bool // Agent is final: "" means no agent rather than the parent's
	Runner      string
	RunnerSet   bool // Runner is final: "" means default runner rather than parent's
	ParentKey   string
	Priority    int
	// Source, ExternalKey and ExternalURL come from importers (default source is manual).
	Source      string
	ExternalKey string
	ExternalURL string
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slug lowercases s, collapses non-alphanumerics to "-" and truncates to max.
func slug(s string, max int) string {
	s = strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > max {
		s = strings.Trim(s[:max], "-")
	}
	if s == "" {
		s = "task"
	}
	return s
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// CreateTask validates the repos and creates the task.
func (s *Service) CreateTask(in NewTaskInput) (store.Task, error) {
	if strings.TrimSpace(in.Title) == "" {
		return store.Task{}, fmt.Errorf("title is required")
	}
	if in.Runner != "" {
		if err := runner.ValidateRunner(in.Runner); err != nil {
			return store.Task{}, err
		}
	}
	var parent *store.Task
	if in.ParentKey != "" {
		p, err := store.GetTaskByKey(s.DB, in.ParentKey)
		if err != nil {
			return store.Task{}, fmt.Errorf("parent: %w", err)
		}
		parent = &p
	}
	if len(in.Repos) == 0 && parent != nil {
		// A sub-task defaults to its parent's repos and primary.
		for _, r := range parent.Repos {
			in.Repos = append(in.Repos, r.RepoName)
			if r.IsPrimary && in.Primary == "" {
				in.Primary = r.RepoName
			}
		}
	}
	if in.Agent == "" && !in.AgentSet && parent != nil && parent.Agent != nil {
		in.Agent = *parent.Agent // like repos: a sub-task starts with its parent's agent
	}
	if in.Runner == "" && !in.RunnerSet && parent != nil && parent.Runner != nil {
		in.Runner = *parent.Runner
	}
	if len(in.Repos) == 0 {
		return store.Task{}, fmt.Errorf("at least one repo is required")
	}
	primary := in.Primary
	if primary == "" {
		primary = in.Repos[0]
	}
	sl := slug(in.Title, 40)
	var links []store.TaskRepo
	seen := map[string]bool{}
	foundPrimary := false
	for _, name := range in.Repos {
		if seen[name] {
			continue
		}
		seen[name] = true
		r, err := store.GetRepoByName(s.DB, name)
		if err != nil {
			return store.Task{}, fmt.Errorf("repo %q is not registered: %w", name, err)
		}
		isPrimary := name == primary
		foundPrimary = foundPrimary || isPrimary
		links = append(links, store.TaskRepo{RepoID: r.ID, IsPrimary: isPrimary, Branch: sl})
	}
	if !foundPrimary {
		return store.Task{}, fmt.Errorf("primary repo %q is not among the task's repos", primary)
	}
	t := store.Task{
		Title: in.Title, Description: in.Description, Priority: in.Priority,
		Model: optional(in.Model), Agent: optional(in.Agent), Runner: optional(in.Runner), Status: store.StatusBacklog,
		Source: in.Source, ExternalKey: optional(in.ExternalKey), ExternalURL: optional(in.ExternalURL),
	}
	if in.Start {
		t.Status = store.StatusTodo
	}
	if parent != nil {
		t.ParentID = &parent.ID
	}
	return store.CreateTask(s.DB, t, links)
}

// AfterMerge is what a comment does to a task with a merged PR.
type AfterMerge string

const (
	FollowUp AfterMerge = ""     // start a follow-up sub-task (the default)
	Ask      AfterMerge = "ask"  // requeue the task to answer, without changing code
	Note     AfterMerge = "note" // only add the comment
)

// AddComment appends a comment. A human or system comment on a task in
// needs_you, review, failed or done requeues it as todo; once one of its
// PRs is merged, after decides instead (see AfterMerge).
func (s *Service) AddComment(key, author, body string, after AfterMerge) (store.Comment, error) {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return store.Comment{}, err
	}
	c, err := store.AddComment(s.DB, store.Comment{TaskID: t.ID, Author: author, Body: body})
	if err != nil {
		return store.Comment{}, err
	}
	var actor store.Actor
	switch author {
	case store.AuthorHuman:
		actor = store.ActorHuman
	case store.AuthorSystem:
		actor = store.ActorSystem
	default:
		return c, nil
	}
	switch t.Status {
	case store.StatusNeedsYou, store.StatusReview, store.StatusFailed, store.StatusDone:
		if pr := mergedPR(t); pr != "" {
			switch after {
			case Note:
				return c, nil
			case Ask:
				note := fmt.Sprintf("%s is merged: answer the comment above without changing code or opening a PR. If it needs changes, say so; the human will start a follow-up.", pr)
				if _, err := store.AddComment(s.DB, store.Comment{TaskID: t.ID, Author: store.AuthorSystem, Body: note}); err != nil {
					return c, err
				}
			default:
				return c, s.followUp(t, pr, body, actor)
			}
		}
		if _, err := store.UpdateTaskStatus(s.DB, t.ID, store.StatusTodo, actor); err != nil {
			return c, fmt.Errorf("requeue after comment: %w", err)
		}
	}
	return c, nil
}

// mergedPR is the URL of one of the task's merged PRs, or "".
func mergedPR(t store.Task) string {
	for _, r := range t.Repos {
		if r.PRState != nil && *r.PRState == "MERGED" && r.PRURL != nil {
			return *r.PRURL
		}
	}
	return ""
}

// MergedPR is the merged PR a comment on the task would trip over (see
// AfterMerge), or "" when the comment just requeues or waits.
func (s *Service) MergedPR(key string) (string, error) {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return "", err
	}
	switch t.Status {
	case store.StatusNeedsYou, store.StatusReview, store.StatusFailed, store.StatusDone:
		return mergedPR(t), nil
	}
	return "", nil
}

// followUp continues a task whose PR is merged in a new todo sub-task, so every
// task keeps one branch and at most one PR per repo. The comment becomes the
// sub-task's description; its brief carries the parent's context. The parent
// is closed as done (best effort: some moves to done are human-only).
func (s *Service) followUp(t store.Task, pr, body string, actor store.Actor) error {
	line, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	title := []rune(strings.Join(strings.Fields(line), " "))
	if len(title) > 60 {
		title = append(title[:57], '…')
	}
	child, err := s.CreateTask(NewTaskInput{Title: string(title), Description: body, ParentKey: t.Key, Source: "followup", Start: true})
	if err != nil {
		return fmt.Errorf("follow-up task: %w", err)
	}
	note := fmt.Sprintf("%s is merged, so this comment continues in %s on a fresh branch: a task keeps one branch and at most one PR per repo.", pr, child.Key)
	if _, err := store.AddComment(s.DB, store.Comment{TaskID: t.ID, Author: store.AuthorSystem, Body: note}); err != nil {
		return err
	}
	if _, err := store.AddComment(s.DB, store.Comment{TaskID: child.ID, Author: store.AuthorSystem,
		Body: fmt.Sprintf("Follow-up of %s, whose PR %s is merged. The brief carries %s's context.", t.Key, pr, t.Key)}); err != nil {
		return err
	}
	if t.Status != store.StatusDone {
		store.UpdateTaskStatus(s.DB, t.ID, store.StatusDone, actor)
	}
	return nil
}

// Move changes a task's status as a human. Moving to done or cancelled also stops
// and removes the task's background sessions (best effort).
func (s *Service) Move(key string, to store.Status) error {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return err
	}
	if _, err = store.UpdateTaskStatus(s.DB, t.ID, to, store.ActorHuman); err != nil {
		return err
	}
	if to == store.StatusDone || to == store.StatusCancelled {
		s.CleanupSessions(t.ID)
	}
	return nil
}

// bgIDs returns the bg ids of the task's runs, each once.
func (s *Service) bgIDs(taskID int64) []string {
	runs, _ := store.ListRuns(s.DB, taskID)
	seen := map[string]bool{}
	var ids []string
	for _, r := range runs {
		if r.BgID != nil && !seen[*r.BgID] {
			seen[*r.BgID] = true
			ids = append(ids, *r.BgID)
		}
	}
	return ids
}

// RunnerForTask returns the appropriate runner backend for a task.
func (s *Service) RunnerForTask(t store.Task) runner.Runner {
	taskRunner := ""
	if t.Runner != nil {
		taskRunner = *t.Runner
	}
	return runner.ForTask(taskRunner, store.GetSetting(s.DB, "default_runner"), s.Home)
}

// CleanupSessions stops and removes the task's background sessions (best effort).
func (s *Service) CleanupSessions(taskID int64) {
	t, err := store.GetTask(s.DB, taskID)
	var bgRunner runner.Runner
	if err == nil {
		bgRunner = s.RunnerForTask(t)
	} else {
		bgRunner = runner.NewClaudeRunner()
	}
	for _, id := range s.bgIDs(taskID) {
		bgRunner.Stop(context.Background(), id)
		bgRunner.Remove(context.Background(), id)
	}
}

// Stop stops the task's background sessions.
func (s *Service) Stop(key string) error {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return err
	}
	bgRunner := s.RunnerForTask(t)
	runs, err := store.ActiveRuns(s.DB)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.TaskID == t.ID && r.BgID != nil {
			if err := bgRunner.Stop(context.Background(), *r.BgID); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetTaskRunner sets (or with "" clears) the task's runner override.
func (s *Service) SetTaskRunner(key, runnerName string) error {
	if err := runner.ValidateRunner(runnerName); err != nil {
		return err
	}
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return err
	}
	t.Runner = optional(runnerName)
	return store.UpdateTask(s.DB, t)
}

// MarkDone moves a task to done.
func (s *Service) MarkDone(key string) error { return s.Move(key, store.StatusDone) }

// Cancel moves a task to cancelled and stops its sessions.
func (s *Service) Cancel(key string) error { return s.Move(key, store.StatusCancelled) }

// SetTaskRepo sets the branch and PR URL of the task's link to repo. The branch
// can only change before the worktree exists; an empty prURL clears the PR.
func (s *Service) SetTaskRepo(key, repo, branch, prURL string) error {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return err
	}
	branch, prURL = strings.TrimSpace(branch), strings.TrimSpace(prURL)
	if branch == "" {
		return fmt.Errorf("branch is required")
	}
	for _, r := range t.Repos {
		if r.RepoName != repo {
			continue
		}
		if branch != r.Branch && r.WorktreePath != nil && *r.WorktreePath != "" {
			return fmt.Errorf("branch can't change once the worktree exists (%s)", *r.WorktreePath)
		}
		r.Branch = branch
		if prURL != deref(r.PRURL) {
			r.PRURL, r.PRState = optional(prURL), nil // state is refreshed by sync
		}
		return store.UpdateTaskRepo(s.DB, r)
	}
	return fmt.Errorf("task %s has no repo %q", key, repo)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
