package core

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JayaSuryaOolio/conveyor/internal/brief"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JayaSuryaOolio/conveyor/internal/claude"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
	"github.com/JayaSuryaOolio/conveyor/internal/terminal"
	"github.com/JayaSuryaOolio/conveyor/internal/transcript"
	"github.com/JayaSuryaOolio/conveyor/internal/workspace"
	"github.com/JayaSuryaOolio/conveyor/skills"
)

// Action is one thing the operator can open from the Workspace panel.
// Kind is attach, resume, fresh or shell.
type Action struct {
	Label string `json:"label"`
	Repo  string `json:"repo"`
	Cwd   string `json:"cwd"`
	Cmd   string `json:"cmd"`
	Kind  string `json:"kind"`
}

// Session is a manual Claude session found under ~/.claude/projects.
type Session struct {
	ID     string `json:"id"`
	Repo   string `json:"repo"`
	MTime  string `json:"mtime"`
	Prompt string `json:"prompt"`
	Action Action `json:"action"`
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func isActive(r store.Run) bool { return r.Status == "launching" || r.Status == "running" }

// settingsPath makes sure the runner's settings file exists and returns its path.
func (s *Service) settingsPath(t store.Task) (string, error) {
	bin, err := s.bin()
	if err != nil {
		return "", err
	}
	r := s.RunnerForTask(t)
	return r.WriteSettings(s.Home, bin)
}

// bin is the conveyor CLI the hooks call: Bin if set, else this executable.
func (s *Service) bin() (string, error) {
	if s.Bin != "" {
		return s.Bin, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find conveyor binary: %w", err)
	}
	return exe, nil
}

// UserHooks reports or changes Conveyor's hooks in ~/.claude/settings.json.
// action is "status", "install" or "uninstall"; it returns the resulting status.
func (s *Service) UserHooks(action string) (string, error) {
	path, err := claude.UserSettingsPath()
	if err != nil {
		return "", err
	}
	bin, err := s.bin()
	if err != nil {
		return "", err
	}
	epic, err := fs.Sub(skills.FS, "epic")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(filepath.Dir(path), "skills", "epic") // ~/.claude/skills/epic
	switch action {
	case "status":
	case "install", "uninstall":
		if err := claude.SetUserHooks(path, bin, s.Home, action == "install"); err != nil {
			return "", err
		}
		if err := claude.SetSkill(epic, dir, action == "install"); err != nil {
			return "", fmt.Errorf("epic skill: %w", err)
		}
		if action == "install" { // seed the agent epic roots run under; never overwrites an edited one
			if _, err := os.Stat(filepath.Join(userAgentsDir(), "epic-builder.md")); err != nil {
				if err := s.AddAgent("epic-builder", "Runs a Conveyor epic: plans it, waits at the gate, drives its sub-tasks to PRs.", "You run an epic. Load the `epic` skill and follow it.", "user", ""); err != nil {
					return "", fmt.Errorf("epic-builder agent: %w", err)
				}
			}
		}
	default:
		return "", fmt.Errorf("unknown hooks action %q", action)
	}
	st, err := claude.UserHooksStatus(path, bin, s.Home)
	if err != nil {
		return "", err
	}
	return claude.SkillStatus(st, epic, dir), nil
}

// Actions lists what can be opened for a task. Without a primary worktree it returns none.
func (s *Service) Actions(key string) ([]Action, error) {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return nil, err
	}
	cwd := workspace.PrimaryPath(t)
	if cwd == "" {
		return []Action{}, nil
	}
	bgRunner := s.RunnerForTask(t)
	settings, err := s.settingsPath(t)
	if err != nil {
		return nil, err
	}
	var primary string
	for _, r := range t.Repos {
		if r.IsPrimary {
			primary = r.RepoName
		}
	}
	var out []Action
	run, err := store.LatestRun(s.DB, t.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	switch {
	case err != nil:
	case isActive(run) && run.BgID != nil:
		out = append(out, Action{"Attach to session", primary, cwd, bgRunner.AttachCommand(*run.BgID), "attach"})
	case !isActive(run) && run.SessionID != nil:
		out = append(out, Action{"Resume in terminal", primary, cwd, bgRunner.ResumeCommand(*run.SessionID, settings, workspace.OtherPaths(t)), "resume"})
	}
	briefPath, err := brief.Render(s.DB, s.Home, t)
	if err != nil {
		return nil, err
	}
	out = append(out, Action{"New session from brief", primary, cwd, bgRunner.FreshCommand(settings, briefPath, workspace.OtherPaths(t)), "fresh"})
	for _, r := range t.Repos {
		if r.WorktreePath == nil {
			continue
		}
		out = append(out, Action{"Shell in " + r.RepoName, r.RepoName, *r.WorktreePath, "", "shell"})
	}
	runs, err := store.ListRuns(s.DB, t.ID)
	if err != nil {
		return nil, err
	}
	// Kinds carry the run id, not the session id: a resume run shares its session id with the run it resumed.
	for i, r := range runs {
		id := strconv.FormatInt(r.ID, 10)
		switch {
		case r.SessionID == nil:
		case !isActive(r):
			out = append(out, Action{fmt.Sprintf("Resume run #%d", i+1), primary, cwd, bgRunner.ResumeCommand(*r.SessionID, settings, workspace.OtherPaths(t)), "resume:" + id})
		case r.BgID != nil: // a run that is not the latest, e.g. blocked on a question, still needs its attach icon
			out = append(out, Action{fmt.Sprintf("Attach to run #%d", i+1), primary, cwd, bgRunner.AttachCommand(*r.BgID), "attach:" + id})
		}
	}
	return out, nil
}

var nonAlnumPath = regexp.MustCompile(`[^a-zA-Z0-9]`)

func (s *Service) projectsDir() string {
	if s.ProjectsDir != "" {
		return s.ProjectsDir
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude", "projects")
}

// firstPrompt returns the first real user prompt of a session transcript, truncated to 80 chars.
func firstPrompt(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		var m struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &m) == nil && m.Type == "user" {
			var text string
			if json.Unmarshal(m.Message.Content, &text) != nil {
				var parts []struct{ Text string }
				json.Unmarshal(m.Message.Content, &parts)
				for _, p := range parts {
					text += p.Text
				}
			}
			if text = strings.TrimSpace(text); text != "" && !strings.HasPrefix(text, "<") {
				if rs := []rune(text); len(rs) > 80 {
					text = string(rs[:80])
				}
				return text
			}
		}
		if err != nil {
			return ""
		}
	}
}

// Sessions returns manual Claude sessions in the task's worktrees (newest first),
// leaving out the sessions Conveyor's own runs already show.
func (s *Service) Sessions(key string) ([]Session, error) {
	t, err := store.GetTaskByKey(s.DB, key)
	if err != nil {
		return nil, err
	}
	runs, err := store.ListRuns(s.DB, t.ID)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, r := range runs {
		if r.SessionID != nil {
			known[*r.SessionID] = true
		}
	}
	settings, err := s.settingsPath(t)
	if err != nil {
		return nil, err
	}
	bgRunner := s.RunnerForTask(t)
	out := []Session{}
	for _, r := range t.Repos {
		if r.WorktreePath == nil {
			continue
		}
		var others []string // the task's other worktrees, re-added on resume
		for _, o := range t.Repos {
			if o.WorktreePath != nil && *o.WorktreePath != *r.WorktreePath {
				others = append(others, *o.WorktreePath)
			}
		}
		dir := filepath.Join(s.projectsDir(), nonAlnumPath.ReplaceAllString(*r.WorktreePath, "-"))
		files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		for _, f := range files {
			id := strings.TrimSuffix(filepath.Base(f), ".jsonl")
			fi, err := os.Stat(f)
			if known[id] || err != nil {
				continue
			}
			out = append(out, Session{
				ID: id, Repo: r.RepoName, MTime: fi.ModTime().UTC().Format(time.RFC3339), Prompt: firstPrompt(f),
				Action: Action{"Resume " + id[:min(8, len(id))], r.RepoName, *r.WorktreePath, bgRunner.ResumeCommand(id, settings, others), "resume"},
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MTime > out[j].MTime })
	return out, nil
}

// RunLog is a page of a run's session chat; pass Next as after to get only newer entries.
type RunLog struct {
	Entries []transcript.Entry `json:"entries"`
	Next    int64              `json:"next"`
}

// RunLog reads the run's Claude or Antigravity transcript from byte offset after.
// A run without a session or transcript yet returns no entries.
func (s *Service) RunLog(runID, after int64) (RunLog, error) {
	r, err := store.GetRun(s.DB, runID)
	if err != nil {
		return RunLog{}, err
	}
	out := RunLog{Entries: []transcript.Entry{}, Next: after}
	if r.SessionID == nil || *r.SessionID == "" {
		return out, nil
	}
	var transcriptFile string
	files, _ := filepath.Glob(filepath.Join(s.projectsDir(), "*", *r.SessionID+".jsonl"))
	if len(files) > 0 {
		transcriptFile = files[0]
	} else if h, err := os.UserHomeDir(); err == nil {
		agyPath := filepath.Join(h, ".gemini", "antigravity-cli", "brain", *r.SessionID, ".system_generated", "logs", "transcript.jsonl")
		if _, err := os.Stat(agyPath); err == nil {
			transcriptFile = agyPath
		}
	}
	if transcriptFile == "" {
		return out, nil
	}
	es, next, err := transcript.Log(transcriptFile, after)
	if err != nil {
		return out, err
	}
	return RunLog{es, next}, nil
}

// FindAction picks an action by kind and repo ("" means the primary repo for shell).
// Kind "resume:<id>" selects a manual session's resume action, or a run's ("resume:<run id>").
func (s *Service) FindAction(key, kind, repo string) (Action, error) {
	if id, ok := strings.CutPrefix(kind, "resume:"); ok {
		ss, err := s.Sessions(key)
		if err != nil {
			return Action{}, err
		}
		for _, x := range ss {
			if x.ID == id {
				return x.Action, nil
			}
		}
	}
	as, err := s.Actions(key)
	if err != nil {
		return Action{}, err
	}
	if len(as) == 0 {
		return Action{}, fmt.Errorf("%s has no workspace yet", key)
	}
	for _, a := range as {
		if a.Kind != kind {
			continue
		}
		if repo == "" && kind == "shell" {
			repo = as[0].Repo // the primary
		}
		if repo == "" || a.Repo == repo {
			return a, nil
		}
	}
	return Action{}, fmt.Errorf("%s: no %q action available", key, kind)
}

// Render is the command line that Launch would run, for --print.
func (s *Service) Render(a Action) string {
	return terminal.Render(store.GetSetting(s.DB, "terminal_template"), a.Cwd, a.Cmd)
}

// Launch runs the action in a terminal.
func (s *Service) Launch(a Action) error {
	return terminal.Open(store.GetSetting(s.DB, "terminal_template"), a.Cwd, a.Cmd)
}

// RunAction finds and launches an action.
func (s *Service) RunAction(key, kind, repo string) error {
	a, err := s.FindAction(key, kind, repo)
	if err != nil {
		return err
	}
	return s.Launch(a)
}
