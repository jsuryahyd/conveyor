// Package learning curates proposed learnings and turns accepted ones into "apply learnings" tasks.
package learning

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayaSuryaOolio/conveyor/internal/core"
	"github.com/JayaSuryaOolio/conveyor/internal/runner"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

// DefaultTarget is the legacy default target file.
const DefaultTarget = "CLAUDE.md"

// DefaultTargetForRunner returns the instruction filename for a runner.
func DefaultTargetForRunner(r string) string {
	return runner.InstructionFileForRunner(r)
}

// TargetForRepo determines the target documentation file for a repo based on disk files
// and the configured runner.
func TargetForRepo(db *sql.DB, repoID *int64) string {
	if repoID != nil {
		r, err := store.GetRepo(db, *repoID)
		if err == nil && r.Path != "" {
			hasAgents := fileExists(filepath.Join(r.Path, "AGENTS.md"))
			hasClaude := fileExists(filepath.Join(r.Path, "CLAUDE.md"))
			if hasAgents && !hasClaude {
				return "AGENTS.md"
			}
			if hasClaude && !hasAgents {
				return "CLAUDE.md"
			}
		}
	}
	r := store.GetSetting(db, "default_runner")
	return DefaultTargetForRunner(r)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// List returns learnings, optionally filtered by status.
func List(db *sql.DB, status ...string) ([]store.Learning, error) {
	return store.ListLearnings(db, status...)
}

// Accept marks a learning accepted for target (default resolved from repo / runner).
func Accept(db *sql.DB, id int64, target string) error {
	l, err := store.GetLearning(db, id)
	if err != nil {
		return err
	}
	if target = strings.TrimSpace(target); target == "" {
		target = TargetForRepo(db, l.RepoID)
	}
	l.Status, l.Target = store.LearningAccepted, &target
	return store.UpdateLearning(db, l)
}

// Reject marks a learning rejected.
func Reject(db *sql.DB, id int64) error {
	l, err := store.GetLearning(db, id)
	if err != nil {
		return err
	}
	l.Status = store.LearningRejected
	return store.UpdateLearning(db, l)
}

// Edit replaces a learning's body.
func Edit(db *sql.DB, id int64, body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("learning body is empty")
	}
	l, err := store.GetLearning(db, id)
	if err != nil {
		return err
	}
	l.Body = strings.TrimSpace(body)
	return store.UpdateLearning(db, l)
}

// Personal returns accepted learnings with no repo, for the human to paste into ~/.claude/CLAUDE.md.
func Personal(db *sql.DB) ([]store.Learning, error) {
	all, err := store.ListLearnings(db, store.LearningAccepted)
	var out []store.Learning
	for _, l := range all {
		if l.RepoID == nil {
			out = append(out, l)
		}
	}
	return out, err
}

const instruction = "Edit the target files so each lesson is captured concisely and isn't duplicated; open one PR."

// Apply creates one todo task "Apply learnings to <repo>" per repo that has accepted learnings (repo "" means every
// repo, otherwise just that one) and marks those learnings applied. Personal learnings are skipped (see Personal).
// It returns the created task keys.
func Apply(svc *core.Service, repo string) ([]string, error) {
	repos, err := store.ListRepos(svc.DB)
	if err != nil {
		return nil, err
	}
	if repo != "" {
		if _, err := store.GetRepoByName(svc.DB, repo); err != nil {
			return nil, err
		}
	}
	accepted, err := store.ListLearnings(svc.DB, store.LearningAccepted)
	if err != nil {
		return nil, err
	}
	byRepo := map[int64][]store.Learning{}
	for _, l := range accepted {
		if l.RepoID != nil {
			byRepo[*l.RepoID] = append(byRepo[*l.RepoID], l)
		}
	}
	var keys []string
	for _, r := range repos {
		ls := byRepo[r.ID]
		if len(ls) == 0 || (repo != "" && r.Name != repo) {
			continue
		}
		var sb strings.Builder
		sb.WriteString("Accepted learnings for " + r.Name + ":\n\n")
		for _, l := range ls {
			src := "unknown task"
			if l.TaskID != nil {
				if t, err := store.GetTask(svc.DB, *l.TaskID); err == nil {
					src = t.Key
				}
			}
			target := TargetForRepo(svc.DB, l.RepoID)
			if l.Target != nil && *l.Target != "" {
				target = *l.Target
			}
			fmt.Fprintf(&sb, "- %s (target: %s, from %s)\n", l.Body, target, src)
		}
		sb.WriteString("\n" + instruction + "\n")
		title := "Apply learnings to " + r.Name
		t, found, err := findPending(svc.DB, title, sb.String())
		if err != nil {
			return keys, err
		}
		if !found {
			defRunner := store.GetSetting(svc.DB, "default_runner")
			if t, err = svc.CreateTask(core.NewTaskInput{
				Title: title, Description: sb.String(), Repos: []string{r.Name}, Start: true, Source: "learning", Runner: defRunner,
			}); err != nil {
				return keys, err
			}
		}
		for _, l := range ls {
			l.Status, l.AppliedTaskID = store.LearningApplied, &t.ID
			if err := store.UpdateLearning(svc.DB, l); err != nil {
				return keys, err
			}
		}
		keys = append(keys, t.Key)
	}
	return keys, nil
}

// findPending returns a live learning task with exactly this title and description: one a previous Apply created
// before failing to mark its learnings applied. Reusing it keeps a retry from creating a duplicate.
func findPending(db *sql.DB, title, desc string) (store.Task, bool, error) {
	tasks, err := store.ListTasks(db)
	if err != nil {
		return store.Task{}, false, err
	}
	for _, t := range tasks {
		if t.Source == "learning" && t.Title == title && t.Description == desc &&
			t.Status != store.StatusDone && t.Status != store.StatusCancelled {
			return t, true, nil
		}
	}
	return store.Task{}, false, nil
}
