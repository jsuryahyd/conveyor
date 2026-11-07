package core

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/JayaSuryaOolio/conveyor/internal/agy"
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
	RunnerSet   bool // Runner is final: "" means default runner rather than the parent's
	ParentKey   string
	Priority    int
	// Source, ExternalKey and ExternalURL come from importers (default source is manual).
	Source      string
	ExternalKey string
	ExternalURL string
	// CreatedBy is stored as tasks.created_by; empty means "human".
	CreatedBy string
	// Epic makes the task an epic root named EpicName (default: its lower-cased key).
	Epic     bool
	EpicName string
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
		in.Runner = *parent.Runner // sub-task starts with parent's runner
	}
	if in.Epic && in.Agent == "" && !in.AgentSet { // epic roots run under the preset `hooks install` seeds
		if _, err := os.Stat(filepath.Join(userAgentsDir(), "epic-builder.md")); err == nil {
			in.Agent = "epic-builder"
		}
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
		Source: in.Source, CreatedBy: optional(cmp.Or(in.CreatedBy, "human")), ExternalKey: optional(in.ExternalKey), ExternalURL: optional(in.ExternalURL),
	}
	if in.Start {
		t.Status = store.StatusTodo
	}
	if parent != nil {
		t.ParentID = &parent.ID
	}
	if in.Epic && in.EpicName != "" { // fail before creating, not after
		other, err := store.EpicNameTaken(s.DB, 0, in.EpicName)
		if err != nil {
			return store.Task{}, err
		}
		if other != "" {
			return store.Task{}, fmt.Errorf("epic name %q is taken by %s", in.EpicName, other)
		}
	}
	created, err := store.CreateTask(s.DB, t, links)
	if err != nil {
		return created, err
	}
	if in.Epic {
		if err := store.MakeEpic(s.DB, created.ID, cmp.Or(in.EpicName, strings.ToLower(created.Key))); err != nil {
			return created, err
		}
	}
	res, err := store.GetTask(s.DB, created.ID)
	if err != nil {
		return res, err
	}
	if in.Runner == "agy" || (in.Runner == "" && s.RunnerForTask(res).Name() == "agy") {
		var roots []string
		for _, tr := range res.Repos {
			if repo, err := store.GetRepoByName(s.DB, tr.RepoName); err == nil {
				roots = append(roots, repo.Path)
			}
		}
		_ = s.TrustAgyWorkspaces(roots...)
	}
	return res, nil
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
	return s.AddCommentVia(key, author, "", body, after)
}

// AddCommentVia is AddComment recording where the comment came from (e.g. "slack").
func (s *Service) AddCommentVia(key, author, source, body string, after AfterMerge) (store.Comment, error) {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return store.Comment{}, err
	}
	c, err := store.AddComment(s.DB, store.Comment{TaskID: t.ID, Author: author, Source: source, Body: body})
	if err != nil {
		return store.Comment{}, err
	}
	if author == store.AuthorHuman && strings.EqualFold(strings.TrimSpace(body), "approve") {
		if gate, _ := store.EpicAtGate(s.DB, t); gate {
			if _, err := store.ApproveEpic(s.DB, t.ID); err != nil {
				return c, err
			}
		}
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
	child, err := s.CreateTask(NewTaskInput{Title: string(title), Description: body, ParentKey: t.Key, Source: "followup", CreatedBy: "followup:" + t.Key, Start: true})
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

// ActiveRunsError refuses a move to review or done while the task still has
// active background runs; the caller confirms, then moves again with stop.
type ActiveRunsError struct {
	Key string
	N   int
}

func (e *ActiveRunsError) Error() string {
	return fmt.Sprintf("%s has %d running run(s); moving it ends them", e.Key, e.N)
}

// Move changes a task's status as a human. Moving to review or done with active
// background runs returns *ActiveRunsError unless stop is set, which ends those
// runs first. Moving to done or cancelled also stops and removes the task's
// background sessions (best effort).
func (s *Service) Move(key string, to store.Status, stop bool) error {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return err
	}
	if err := store.ValidateTransition(t.Status, to, store.ActorHuman); err != nil {
		return err
	}
	if to == store.StatusReview || to == store.StatusDone {
		runs, err := s.autoRuns(t.ID)
		if err != nil {
			return err
		}
		if len(runs) > 0 && !stop {
			return &ActiveRunsError{Key: key, N: len(runs)}
		}
		// End runs before the move, so a late hook of a killed session can't move the task back.
		if err := s.endRuns(runs); err != nil {
			return err
		}
	}
	if _, err = store.UpdateTaskStatus(s.DB, t.ID, to, store.ActorHuman); err != nil {
		return err
	}
	if to == store.StatusDone || to == store.StatusCancelled {
		s.CleanupSessions(t.ID)
	}
	return nil
}

// autoRuns returns the task's active runs Conveyor launched; manual
// (interactive) sessions are the human's and are left alone.
func (s *Service) autoRuns(taskID int64) ([]store.Run, error) {
	runs, err := store.ActiveRuns(s.DB)
	var out []store.Run
	for _, r := range runs {
		if r.TaskID == taskID && r.Kind != "interactive" {
			out = append(out, r)
		}
	}
	return out, err
}

// endRuns marks runs failed ("stopped") then stops and removes their Claude
// sessions (best effort). Store first, so the reaper never sees a vanished
// session of a still-active run.
func (s *Service) endRuns(runs []store.Run) error {
	ended := time.Now().UTC().Format(time.RFC3339)
	for _, r := range runs {
		r.Status, r.Error, r.EndedAt = "failed", &stoppedReason, &ended
		if err := store.UpdateRun(s.DB, r); err != nil {
			return err
		}
	}
	for _, r := range runs {
		if r.BgID != nil {
			t, err := store.GetTask(s.DB, r.TaskID)
			var bgRunner runner.Runner
			if err == nil {
				bgRunner = s.RunnerForTask(t)
			} else {
				bgRunner = runner.NewClaudeRunner()
			}
			bgRunner.Stop(context.Background(), *r.BgID)
			bgRunner.Remove(context.Background(), *r.BgID)
		}
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

// TrustAgyWorkspaces updates ~/.gemini/antigravity-cli/settings.json to add
// repo roots (or all registered repos if none specified), workspaces, and ~/.config/gh.
func (s *Service) TrustAgyWorkspaces(repoRoots ...string) error {
	if len(repoRoots) == 0 {
		repos, _ := store.ListRepos(s.DB)
		for _, r := range repos {
			repoRoots = append(repoRoots, r.Path)
		}
	}
	return agy.AddTrustedWorkspaces(s.Home, repoRoots...)
}

// SetSetting stores one setting and applies side effects (e.g. updating trustedWorkspaces for agy runner).
func (s *Service) SetSetting(k, v string) error {
	if err := store.SetSetting(s.DB, k, v); err != nil {
		return err
	}
	if k == "default_runner" && v == "agy" {
		_ = s.TrustAgyWorkspaces()
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
	if err := store.UpdateTask(s.DB, t); err != nil {
		return err
	}
	if runnerName == "agy" || (runnerName == "" && s.RunnerForTask(t).Name() == "agy") {
		var roots []string
		for _, tr := range t.Repos {
			if repo, err := store.GetRepoByName(s.DB, tr.RepoName); err == nil {
				roots = append(roots, repo.Path)
			}
		}
		_ = s.TrustAgyWorkspaces(roots...)
	}
	return nil
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

// Stop ends the task's active background runs as a human: marks them failed
// ("stopped"), moves a running task to needs_you, then stops and removes their
// Claude sessions (best effort). Manual (interactive) sessions are left alone.
func (s *Service) Stop(key string) error {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return err
	}
	runs, err := s.autoRuns(t.ID)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		return fmt.Errorf("%s has no running session to stop", key)
	}
	if err := s.endRuns(runs); err != nil {
		return err
	}
	if t.Status == store.StatusRunning {
		if _, err := store.AddComment(s.DB, store.Comment{TaskID: t.ID, Author: store.AuthorSystem, Body: "A human stopped the run."}); err != nil {
			return err
		}
		if _, err := store.UpdateTaskStatus(s.DB, t.ID, store.StatusNeedsYou, store.ActorSystem); err != nil {
			return err
		}
	}
	return nil
}

var stoppedReason = "stopped"

// MarkDone moves a task to done; stop ends its active runs (see Move).
func (s *Service) MarkDone(key string, stop bool) error { return s.Move(key, store.StatusDone, stop) }

// Cancel moves a task to cancelled and stops its sessions.
func (s *Service) Cancel(key string) error { return s.Move(key, store.StatusCancelled, false) }

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
