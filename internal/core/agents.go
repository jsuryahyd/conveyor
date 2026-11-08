package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JayaSuryaOolio/conveyor/internal/claude"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

// Agent is a Claude Code subagent file. Scope is the repo name or "user".
type Agent struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Scope       string   `json:"scope"`
	Model       string   `json:"model"`
	Skills      []string `json:"skills"` // frontmatter `skills:`, comma separated; shown read-only on epic cards
	// Resolved is the model id Model resolves to. Set by `conveyor agent list`, not ListAgents.
	Resolved string `json:"resolved,omitempty"`
}

var agentName = regexp.MustCompile(`^[a-z0-9-]+$`)

func scanAgents(dir, scope string) []Agent {
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	var out []Agent
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		fm, _ := claude.Frontmatter(b)
		if fm["name"] == "" {
			continue
		}
		out = append(out, Agent{Name: fm["name"], Description: fm["description"], Scope: scope, Model: fm["model"], Skills: splitList(fm["skills"])})
	}
	return out
}

func userAgentsDir() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude", "agents")
}

func geminiAgentsDir() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".gemini", "config", "agents")
}

// ListAgents returns the subagents of the named repos' main checkouts, then the user's.
func (s *Service) ListAgents(repos []string) ([]Agent, error) {
	out := []Agent{}
	seen := map[string]bool{}
	for _, n := range repos {
		if seen[n] {
			continue
		}
		seen[n] = true
		r, err := store.GetRepoByName(s.DB, n)
		if err != nil {
			return nil, fmt.Errorf("repo %q is not registered: %w", n, err)
		}
		out = append(out, scanAgents(filepath.Join(r.Path, ".claude", "agents"), n)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	userAgents := scanAgents(userAgentsDir(), "user")
	userSeen := map[string]bool{}
	for _, a := range userAgents {
		userSeen[a.Name] = true
	}
	for _, a := range scanAgents(geminiAgentsDir(), "user") {
		if !userSeen[a.Name] {
			userSeen[a.Name] = true
			userAgents = append(userAgents, a)
		}
	}
	return append(out, userAgents...), nil
}

// AddAgent writes <repo>/.claude/agents/<name>.md (scope = repo name) or ~/.claude/agents (scope "user").
// It never overwrites an existing file.
func (s *Service) AddAgent(name, description, prompt, scope, model string) error {
	if !agentName.MatchString(name) {
		return fmt.Errorf("agent name %q must match [a-z0-9-]+", name)
	}
	if strings.TrimSpace(description) == "" || strings.ContainsAny(description, "\r\n") {
		return fmt.Errorf("description is required and must be one line")
	}
	if model = strings.TrimSpace(model); strings.ContainsAny(model, "\r\n") {
		return fmt.Errorf("model must be one line")
	}
	dir, err := s.agentsDir(scope)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, name+".md"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("agent %q already exists in %s", name, scope)
		}
		return err
	}
	defer f.Close()
	if model != "" {
		model = "model: " + model + "\n"
	}
	_, err = fmt.Fprintf(f, "---\nname: %s\ndescription: %q\n%s---\n\n%s\n", name, strings.TrimSpace(description), model, strings.TrimSpace(prompt))
	if err == nil && scope == "user" {
		gdir := geminiAgentsDir()
		if _, serr := os.Stat(filepath.Dir(filepath.Dir(gdir))); serr == nil { // ~/.gemini exists
			_ = os.MkdirAll(gdir, 0o755)
			content := fmt.Sprintf("---\nname: %s\ndescription: %q\n%s---\n\n%s\n", name, strings.TrimSpace(description), model, strings.TrimSpace(prompt))
			_ = os.WriteFile(filepath.Join(gdir, name+".md"), []byte(content), 0o644)
		}
	}
	return err
}

func (s *Service) agentsDir(scope string) (string, error) {
	if scope == "user" {
		return userAgentsDir(), nil
	}
	r, err := store.GetRepoByName(s.DB, scope)
	if err != nil {
		return "", fmt.Errorf("repo %q is not registered: %w", scope, err)
	}
	return filepath.Join(r.Path, ".claude", "agents"), nil
}

// setFrontmatterModel rewrites, inserts, or (model "") removes the top-level model: line, leaving the rest as is.
func setFrontmatterModel(b []byte, model string) ([]byte, error) {
	lines := strings.Split(string(b), "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return nil, fmt.Errorf("no frontmatter")
	}
	end, at := -1, -1
	for i := 1; i < len(lines) && end < 0; i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
		} else if strings.HasPrefix(lines[i], "model:") {
			at = i
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("unterminated frontmatter")
	}
	line := "model: " + model
	if strings.HasSuffix(lines[0], "\r") {
		line += "\r"
	}
	switch {
	case at >= 0 && model == "":
		lines = append(lines[:at], lines[at+1:]...)
	case at >= 0:
		lines[at] = line
	case model != "":
		lines = append(lines[:end], append([]string{line}, lines[end:]...)...)
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// SetAgentModel sets (or with "" removes) the model: line of the named agent's file in scope.
func (s *Service) SetAgentModel(scope, name, model string) error {
	if model = strings.TrimSpace(model); strings.ContainsAny(model, "\r\n") {
		return fmt.Errorf("model must be one line")
	}
	dir, err := s.agentsDir(scope)
	if err != nil {
		return err
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if fm, _ := claude.Frontmatter(b); fm["name"] != name {
			continue
		}
		if b, err = setFrontmatterModel(b, model); err != nil {
			return err
		}
		if err := os.WriteFile(f, b, 0o644); err != nil {
			return err
		}
		if scope == "user" {
			syncAgyUserAgentModel(name, model)
		}
		return nil
	}
	return fmt.Errorf("agent %q not found in %s", name, scope)
}

func mapAgySubagentModel(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "sonnet", "opus", "claude-sonnet-4-6", "claude-opus-4-6-thinking":
		return "pro"
	case "haiku":
		return "flash"
	case "flash", "flash_lite", "pro", "inherit":
		return strings.ToLower(strings.TrimSpace(m))
	default:
		return "inherit"
	}
}

func syncAgyUserAgentModel(name, model string) {
	h, err := os.UserHomeDir()
	if err != nil {
		return
	}
	geminiAgent := filepath.Join(h, ".gemini", "config", "agents", name+".md")
	gb, err := os.ReadFile(geminiAgent)
	if err != nil {
		return
	}
	geminiModel := ""
	if model != "" {
		geminiModel = mapAgySubagentModel(model)
	}
	if newGB, err := setFrontmatterModel(gb, geminiModel); err == nil {
		_ = os.WriteFile(geminiAgent, newGB, 0o644)
	}
}

// AgentModel returns the model set by the named agent's file, looking in each dir's .claude/agents, then the user's. "" if none.
func AgentModel(name string, dirs ...string) string {
	for _, d := range dirs {
		for _, a := range scanAgents(filepath.Join(d, ".claude", "agents"), "") {
			if a.Name == name {
				return a.Model
			}
		}
	}
	for _, a := range scanAgents(userAgentsDir(), "") {
		if a.Name == name {
			return a.Model
		}
	}
	return ""
}

// SetTaskAgent sets (or with "" clears) the task's agent.
func (s *Service) SetTaskAgent(key, agent string) error {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return err
	}
	t.Agent = optional(agent)
	return store.UpdateTask(s.DB, t)
}

// SetTaskModel sets (or with "" clears) the task's model override.
func (s *Service) SetTaskModel(key, model string) error {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return err
	}
	t.Model = optional(model)
	return store.UpdateTask(s.DB, t)
}

// SetTaskDescription replaces the task's description. Once a run has seen the
// old one, it also posts a system comment quoting it: the next run (or the
// running one, at its Stop) receives it and re-checks its acceptance criteria,
// and the human sees the edit in the thread.
func (s *Service) SetTaskDescription(key, desc string) error {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return err
	}
	if t.Description == desc {
		return nil
	}
	old := t.Description
	t.Description = desc
	if err := store.UpdateTask(s.DB, t); err != nil {
		return err
	}
	runs, err := store.ListRuns(s.DB, t.ID)
	if err != nil || len(runs) == 0 {
		return err
	}
	quoted := "> (empty)"
	if strings.TrimSpace(old) != "" {
		quoted = "> " + strings.ReplaceAll(strings.TrimSpace(old), "\n", "\n> ")
	}
	note := "The human edited the description; BRIEF.md has the new one. Re-read it and update your acceptance criteria to match.\n\nPrevious description:\n\n" + quoted
	_, err = store.AddComment(s.DB, store.Comment{TaskID: t.ID, Author: store.AuthorSystem, Body: note})
	return err
}

// AddTaskRepo adds a registered repo to an existing task.
func (s *Service) AddTaskRepo(key, repo string) error {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return err
	}
	r, err := store.GetRepoByName(s.DB, repo)
	if err != nil {
		return fmt.Errorf("repo %q is not registered: %w", repo, err)
	}
	for _, tr := range t.Repos {
		if tr.RepoID == r.ID {
			return fmt.Errorf("%s already has repo %q", key, repo)
		}
	}
	return store.AddTaskRepo(s.DB, t.ID, r.ID, "cv/"+t.Key+"-"+slug(t.Title, 40))
}

// RemoveTaskRepo drops a repo from a task unless it has a PR or is the only repo.
// An existing worktree is left on disk. A removed primary hands over to the first remaining repo.
func (s *Service) RemoveTaskRepo(key, repo string) error {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return err
	}
	if len(t.Repos) < 2 {
		return fmt.Errorf("%s needs at least one repo", key)
	}
	for _, tr := range t.Repos {
		if tr.RepoName != repo {
			continue
		}
		if tr.PRURL != nil && *tr.PRURL != "" {
			return fmt.Errorf("%s has a PR in %q; it cannot be removed", key, repo)
		}
		if err := store.RemoveTaskRepo(s.DB, t.ID, tr.RepoID); err != nil {
			return err
		}
		if tr.IsPrimary {
			for _, o := range t.Repos {
				if o.RepoID != tr.RepoID {
					_, err := s.DB.Exec(`UPDATE task_repos SET is_primary=1 WHERE task_id=? AND repo_id=?`, t.ID, o.RepoID)
					return err
				}
			}
		}
		return nil
	}
	return fmt.Errorf("%s has no repo %q", key, repo)
}

// splitList reads "a, b" or "[a, b]" (the flat frontmatter parser keeps lists as one string).
// refactor: claude.Frontmatter could return real YAML lists, then this goes away.
func splitList(v string) []string {
	out := []string{}
	for _, x := range strings.Split(strings.Trim(strings.TrimSpace(v), "[]"), ",") {
		if x = strings.Trim(strings.TrimSpace(x), `"'`); x != "" {
			out = append(out, x)
		}
	}
	return out
}
