// Package brief renders BRIEF.md, the hot-tier context an agent reads first, and the launch prompts.
package brief

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayaSuryaOolio/conveyor/internal/store"
	"github.com/JayaSuryaOolio/conveyor/internal/workspace"
)

const maxComments = 20

const statusBlock = `## Status block

End every final reply with this block. Omit it to ask the human a question instead.

` + "```" + `
<conveyor>
status: done | failed
summary: one paragraph
state: branch ..., PR #N, tests: pass|fail|not run
next: what remains
criteria:
- [x] AC1 - evidence
- [ ] AC2 - why not
learning: only if this repo had a trap that cost you >10 min and is not in CLAUDE.md/AGENTS.md/SKILL.md/LEARNINGS.md. Name the file or command. Omit the line otherwise. (repeatable line)
spawn: [todo] child task title | description with everything its agent needs (repeatable line; [todo] is optional: the child starts once unblocked, else it waits in backlog)
</conveyor>
` + "```" + `
`

// relationsHelp tells the agent how to split work: Conveyor only stores the relations, the agent does the handoff.
const relationsHelp = `
To split work or hand a part off, create a sub-task (` + "`--parent`" + `, key CV-1A) or a sibling (same ` + "`--parent`" + ` as this task); give it ` + "`--repo`" + ` of the repo it must change, which may differ from this task's, and a description with everything its agent needs:

` + "```" + `
conveyor task new "title" --repo <repo> --parent <KEY> --desc "..." [--todo] [--agent <name>]
conveyor task link <KEY> blocks <OTHER>      # OTHER waits until KEY is done or cancelled
conveyor task link <KEY> relates <OTHER>
conveyor task parent <KEY> <PARENT>|--none   # re-parent; keys do not change
conveyor task unlink <KEY> blocks <OTHER>
conveyor task repo <KEY> add <REPO>          # attach another registered repo to a task (worktree appears on its next run)
` + "```" + `

If this task depends on another task's unmerged PR, record it with ` + "`conveyor task link <THAT-KEY> blocks <KEY>`" + ` instead of a learning.

New tasks start in backlog; pass ` + "`--todo`" + ` to let the dispatcher launch them.
`

const defaultRules = `- Work only inside the worktrees listed above. Every one of them is yours, not just the one the session starts in: change whichever the task needs, using absolute paths or `+"`git -C <path>`"+`. For more than a small change in a repo other than the one the session starts in, hand it to a sub-task with `+"`--repo <that repo>`"+` instead: its session starts in that repo and loads its CLAUDE.md, skills, commands and agents, which this session does not fully see.
- Open at most one PR per repo.
- Never push to the default branch.
- The repo's CLAUDE.md, skills, commands and agents are the authority on how to work in it (build, test, commit, PR, changesets). Use them over your own defaults and over generic user-level skills; where they disagree with this brief, follow the repo, except for the worktree, PR and status block rules here.
`

// Built-in prompt templates; empty prompt_fresh / prompt_resume settings use these.
const (
	DefaultFresh  = "You are working on Conveyor task {key}: {title}. Read {brief} first and follow it.\n{comments}"
	DefaultResume = "New comments on {key}:\n{comments}\n(The brief at {brief} is up to date.)"
	// resumeNoComments is DefaultResume's stand-in when the run was requeued without a new comment.
	resumeNoComments = "{key} was resumed with no new comments. Do not re-report your last reply; continue any unfinished work, or end with one line saying nothing changed.\n(The brief at {brief} is up to date.)"
)

// authorityRunner returns the active runner for a task.
func authorityRunner(db *sql.DB, task store.Task) string {
	if task.Runner != nil && *task.Runner != "" {
		return *task.Runner
	}
	return store.GetSetting(db, "default_runner")
}

// rules is the Rules section body: the defaults, the PR comment prefix rule, then (if extra) the user's extra rules.
func rules(db *sql.DB, extra bool) string {
	return rulesForTask(db, store.Task{}, extra)
}

func rulesForTask(db *sql.DB, task store.Task, extra bool) string {
	doc := "CLAUDE.md"
	if authorityRunner(db, task) == "agy" {
		doc = "AGENTS.md"
	}
	r := fmt.Sprintf(`- Work only inside the worktrees listed above. Every one of them is yours, not just the one the session starts in: change whichever the task needs, using absolute paths or `+"`git -C <path>`"+`. For more than a small change in a repo other than the one the session starts in, hand it to a sub-task with `+"`--repo <that repo>`"+` instead: its session starts in that repo and loads its %s, skills, commands and agents, which this session does not fully see.
- Open at most one PR per repo.
- Never push to the default branch.
- The repo's %s, skills, commands and agents are the authority on how to work in it (build, test, commit, PR, changesets). Use them over your own defaults and over generic user-level skills; where they disagree with this brief, follow the repo, except for the worktree, PR and status block rules here.
`, doc, doc)
	if p := store.GetSetting(db, "pr_comment_prefix"); p != "" {
		r += fmt.Sprintf("- Start every comment or reply you post on a PR or issue with `%s`.\n", p)
	}
	if x := strings.TrimSpace(store.GetSetting(db, "brief_extra_rules")); extra && x != "" {
		r += x + "\n"
	}
	return r
}

// Protocol is the status-block protocol and the built-in rules (extra rules excluded), for display.
func Protocol(db *sql.DB) string { return statusBlock + "\n## Rules\n\n" + rules(db, false) }

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Render writes <ws>/BRIEF.md and returns its path.
func Render(db *sql.DB, home string, task store.Task) (string, error) {
	text, err := Build(db, task)
	if err != nil {
		return "", err
	}
	dir := workspace.Dir(home, task.Key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create workspace dir: %w", err)
	}
	path := filepath.Join(dir, "BRIEF.md")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", fmt.Errorf("write brief: %w", err)
	}
	if err := writeLearnings(home, task); err != nil {
		return "", err
	}
	return path, nil
}

// writeLearnings writes <ws>/CLAUDE.md and <ws>/AGENTS.md from each task repo's LEARNINGS.md,
// or removes them when there are none. The workspace folder is an ancestor of the session's cwd,
// so Claude Code loads CLAUDE.md and Antigravity loads AGENTS.md without being told to.
func writeLearnings(home string, task store.Task) error {
	var b strings.Builder
	for _, r := range task.Repos {
		if l, err := os.ReadFile(workspace.LearningsFile(home, r.RepoName)); err == nil && len(strings.TrimSpace(string(l))) > 0 {
			fmt.Fprintf(&b, "# %s learnings\n\n%s\n\n", r.RepoName, strings.TrimSpace(string(l)))
		}
	}
	claudePath := filepath.Join(workspace.Dir(home, task.Key), "CLAUDE.md")
	agentsPath := filepath.Join(workspace.Dir(home, task.Key), "AGENTS.md")
	if b.Len() == 0 {
		for _, p := range []string{claudePath, agentsPath} {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove learnings: %w", err)
			}
		}
		return nil
	}
	for _, p := range []string{claudePath, agentsPath} {
		if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
			return fmt.Errorf("write learnings: %w", err)
		}
	}
	return nil
}

// Path is where Render writes the task's brief.
func Path(home string, task store.Task) string {
	return filepath.Join(workspace.Dir(home, task.Key), "BRIEF.md")
}

// Build renders the brief text without writing anything.
func Build(db *sql.DB, task store.Task) (string, error) {
	runs, err := store.ListRuns(db, task.ID)
	if err != nil {
		return "", err
	}
	comments, err := store.ListComments(db, task.ID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\nKey: %s\n", task.Title, task.Key)
	if err := labelLines(&b, db, task); err != nil {
		return "", err
	}
	b.WriteString("\n## Repos\n\n")
	for _, r := range task.Repos {
		pr := "none"
		if r.PRURL != nil {
			pr = *r.PRURL
		}
		fmt.Fprintf(&b, "- %s: %s (branch `%s`, PR: %s)\n", r.RepoName, deref(r.WorktreePath), r.Branch, pr)
	}
	desc := strings.TrimSpace(task.Description)
	if desc == "" {
		desc = "(none)"
	}
	fmt.Fprintf(&b, "\n## Description\n\n%s\n", desc)
	if task.Source == "followup" && task.ParentID != nil {
		if err := parentContext(&b, db, *task.ParentID); err != nil {
			return "", err
		}
	}
	fmt.Fprintf(&b, "\n## Acceptance criteria\n\n%s\n", criteria(runs, "see description"))
	if w := workflows(db, task); w != "" {
		runnerName := authorityRunner(db, task)
		runnerTitle := "Claude Code"
		doc := "CLAUDE.md"
		if runnerName == "agy" {
			runnerTitle = "Antigravity"
			doc = "AGENTS.md"
		}
		fmt.Fprintf(&b, "\n## Other repos' context\n\n%s loaded only the primary repo's %s, skills, commands and agents. For these repos, read the files named here (`.claude/commands/<name>.md`, `.claude/skills/<name>/SKILL.md`, `.claude/agents/<name>.md`) when the work touches them.\n\n%s", runnerTitle, doc, w)
	}
	history(&b, "##", runs, comments)
	rel, err := store.ListRelations(db, task)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "\n## Relations\n\n")
	if rel.Parent != nil {
		fmt.Fprintf(&b, "- parent: %s %s (%s)\n", rel.Parent.Key, rel.Parent.Title, rel.Parent.Status)
	}
	for _, c := range rel.Children {
		fmt.Fprintf(&b, "- sub-task: %s %s (%s)\n", c.Key, c.Title, c.Status)
	}
	for _, l := range rel.Links {
		fmt.Fprintf(&b, "- %s: %s %s (%s)\n", l.Label, l.Task.Key, l.Task.Title, l.Task.Status)
	}
	b.WriteString(relationsHelp)
	fmt.Fprintf(&b, "\n%s\n## Rules\n\n%s\n## Older history\n\nFull thread: `conveyor task show %s --thread --json`\n", statusBlock, rulesForTask(db, task, true), task.Key)
	return b.String(), nil
}

// kindRules is the one-line rule per epic_kind (docs/epic-builder.md §2.1).
var kindRules = map[string]string{
	"grilling":  "ask the human your questions and wait for the answers, record the decision on the epic root's Decisions so far, open no PR.",
	"prototype": "build a throwaway branch or notes that answer the design question and show it to the human; open no PR to merge.",
	"research":  "read code and docs and report findings in the status block and on the epic root; change no code, open no PR.",
	"build":     "write the failing test first, open one PR, cite the AC ids it covers. Never merge it.",
}

// labelLines writes the Labels line and what the epic labels tell the agent:
// its epic and stage, the rule for its kind and the skills to load.
func labelLines(b *strings.Builder, db *sql.DB, task store.Task) error {
	if len(task.Labels) == 0 {
		return nil
	}
	fmt.Fprintf(b, "Labels: %s\n", strings.Join(task.Labels, ", "))
	if name := store.EpicName(task); name != "" {
		root, err := store.EpicRoot(db, task)
		if err != nil {
			return err
		}
		flow := "none"
		if f, err := store.ActiveFlow(db, root.ID); err != nil {
			return err
		} else if f != nil {
			flow = fmt.Sprint(f.Attempt)
		}
		fmt.Fprintf(b, "Epic %s (root %s), stage %s, flow %s. Load the epic skill and run `conveyor epic status %s` first.\n",
			name, root.Key, store.EpicStage(root), flow, root.Key)
	}
	if k := store.LabelValue(task, store.PrefixKind); kindRules[k] != "" {
		fmt.Fprintf(b, "Kind %s: %s\n", k, kindRules[k])
	}
	var skills []string
	for _, l := range task.Labels {
		if s, ok := strings.CutPrefix(l, "skill:"); ok && s != "" {
			skills = append(skills, s)
		}
	}
	if len(skills) > 0 {
		fmt.Fprintf(b, "Load these skills before starting: %s.\n", strings.Join(skills, ", "))
	}
	return nil
}

// criteria is the latest run's acceptance criteria, or def.
func criteria(runs []store.Run, def string) string {
	for i := len(runs) - 1; i >= 0; i-- {
		if c := strings.TrimSpace(deref(runs[i].Criteria)); c != "" {
			return c
		}
	}
	return def
}

// history writes the latest run, recent run summaries and human comments, with headings at level h.
func history(b *strings.Builder, h string, runs []store.Run, comments []store.Comment) {
	if len(runs) > 0 {
		last := runs[len(runs)-1]
		fmt.Fprintf(b, "\n%s Latest run\n\nState: %s\nNext: %s\n", h, oneLine(deref(last.State)), oneLine(deref(last.Next)))
	}
	var sums []string
	for _, r := range runs {
		if s := oneLine(deref(r.Summary)); s != "" {
			sums = append(sums, fmt.Sprintf("- run %d: %s", r.ID, s))
		}
	}
	if len(sums) > 3 {
		sums = sums[len(sums)-3:]
	}
	if len(sums) > 0 {
		fmt.Fprintf(b, "\n%s Recent runs\n\n%s\n", h, strings.Join(sums, "\n"))
	}
	var human []string
	for _, c := range comments {
		if c.Author == store.AuthorHuman {
			human = append(human, "- "+firstLine(c.Body))
		}
	}
	if len(human) > maxComments {
		human = human[len(human)-maxComments:]
	}
	if len(human) > 0 {
		fmt.Fprintf(b, "\n%s Human comments\n\n%s\n", h, strings.Join(human, "\n"))
	}
}

// parentContext writes the context a follow-up continues from: the parent's
// description, criteria, latest run, run summaries and human comments.
func parentContext(b *strings.Builder, db *sql.DB, parentID int64) error {
	p, err := store.GetTask(db, parentID)
	if err != nil {
		return err
	}
	runs, err := store.ListRuns(db, p.ID)
	if err != nil {
		return err
	}
	comments, err := store.ListComments(db, p.ID)
	if err != nil {
		return err
	}
	var prs []string
	for _, r := range p.Repos {
		if r.PRURL != nil {
			prs = append(prs, fmt.Sprintf("%s (%s)", *r.PRURL, strings.ToLower(deref(r.PRState))))
		}
	}
	fmt.Fprintf(b, "\n## Continues %s: %s\n\nThis task is a follow-up of %s, whose PR is merged (%s). Start from the default branch; the parent's work is already in it. Its context:\n",
		p.Key, p.Title, p.Key, strings.Join(prs, ", "))
	fmt.Fprintf(b, "\n### Its description\n\n%s\n\n### Its acceptance criteria\n\n%s\n", strings.TrimSpace(p.Description), criteria(runs, "(none recorded)"))
	history(b, "###", runs, comments)
	return nil
}

func deltaText(delta []store.Comment) string {
	lines := make([]string, len(delta))
	for i, c := range delta {
		lines[i] = fmt.Sprintf("- [%s] %s", c.Author, c.Body)
	}
	return strings.Join(lines, "\n")
}

func fill(tmpl, def string, task store.Task, briefPath string, delta []store.Comment) string {
	if strings.TrimSpace(tmpl) == "" {
		tmpl = def
	}
	return strings.TrimSpace(strings.NewReplacer("{key}", task.Key, "{title}", task.Title, "{brief}", briefPath, "{comments}", deltaText(delta)).Replace(tmpl))
}

// FreshPrompt is the first prompt of a fresh session; tmpl is the prompt_fresh setting.
func FreshPrompt(task store.Task, briefPath string, delta []store.Comment, tmpl string) string {
	return fill(tmpl, DefaultFresh, task, briefPath, delta)
}

// ResumePrompt is the prompt that resumes a hot session with new comments; tmpl is the prompt_resume setting.
func ResumePrompt(task store.Task, briefPath string, delta []store.Comment, tmpl string) string {
	def := DefaultResume
	if len(delta) == 0 {
		def = resumeNoComments
	}
	return fill(tmpl, def, task, briefPath, delta)
}

// Preview renders the brief and the fresh prompt as a launch would, writing nothing.
func Preview(db *sql.DB, home string, task store.Task) (text, prompt string, err error) {
	if text, err = Build(db, task); err != nil {
		return "", "", err
	}
	delta, err := store.UnconsumedComments(db, task.ID)
	if err != nil {
		return "", "", err
	}
	return text, FreshPrompt(task, Path(home, task), delta, store.GetSetting(db, "prompt_fresh")), nil
}
