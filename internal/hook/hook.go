// Package hook turns Claude Code hook events into store updates (tech-spec §8).
package hook

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JayaSuryaOolio/conveyor/internal/agy"
	"github.com/JayaSuryaOolio/conveyor/internal/core"
	"github.com/JayaSuryaOolio/conveyor/internal/gh"
	"github.com/JayaSuryaOolio/conveyor/internal/statusblock"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
	"github.com/JayaSuryaOolio/conveyor/internal/transcript"
	"github.com/JayaSuryaOolio/conveyor/internal/workspace"
)

type payload struct {
	SessionID                 string `json:"session_id"`
	SessionIDCamel            string `json:"sessionId"`
	ConversationID            string `json:"conversation_id"` // agy alias
	ConversationIDCamel       string `json:"conversationId"`
	Cwd                       string `json:"cwd"`
	TaskKey                   string `json:"task_key"` // optional direct key
	TaskKeyCamel              string `json:"taskKey"`
	TranscriptPath            string `json:"transcript_path"`
	TranscriptPathCamel       string `json:"transcriptPath"`
	LastAssistantMessage      string `json:"last_assistant_message"`
	LastAssistantMessageCamel string `json:"lastAssistantMessage"`
	Message                   string `json:"message"`
	NotificationType          string `json:"notification_type"`
	StopHookActive            bool   `json:"stop_hook_active"`
	FilePath                  string `json:"file_path"` // InstructionsLoaded
	FilePathCamel             string `json:"filePath"`
}

// Handle processes one hook event and returns what to print on stdout (a Stop it blocks), or "".
// Callers log the returned error and still exit 0.
func Handle(db *sql.DB, home, event string, raw []byte) (string, error) {
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse %s payload: %w", event, err)
	}
	if p.SessionID == "" {
		p.SessionID = p.SessionIDCamel
	}
	if p.ConversationID == "" {
		p.ConversationID = p.ConversationIDCamel
	}
	if p.SessionID == "" && p.ConversationID != "" {
		p.SessionID = p.ConversationID
	}
	if p.TaskKey == "" {
		p.TaskKey = p.TaskKeyCamel
	}
	if p.TranscriptPath == "" {
		p.TranscriptPath = p.TranscriptPathCamel
	}
	if p.LastAssistantMessage == "" {
		p.LastAssistantMessage = p.LastAssistantMessageCamel
	}
	if p.FilePath == "" {
		p.FilePath = p.FilePathCamel
	}
	key := workspace.KeyFromCwd(home, p.Cwd)
	if key == "" && p.TaskKey != "" {
		key = p.TaskKey
	}
	var task store.Task
	var err error
	if key == "" && p.SessionID != "" {
		// A bg session can report a stray cwd (seen: /private/tmp on a resume); its session id still names the run.
		if r, rerr := store.RunBySession(db, p.SessionID); rerr == nil {
			task, err = store.GetTask(db, r.TaskID)
			key = task.Key
		}
	} else if key != "" {
		task, err = store.GetTaskByKey(db, key)
	}
	if key == "" {
		return "", nil // not one of ours
	}
	if err != nil {
		return "", fmt.Errorf("hook %s: %w", event, err)
	}
	if event == "InstructionsLoaded" { // read by the completion check: was the repo's CLAUDE.md loaded
		if !strings.HasPrefix(p.FilePath, workspace.Dir(home, key)+"/") {
			return "", nil // user-level and other files: not what the check asks about
		}
		return "", store.RecordEvent(db, task.ID, "instructions_loaded", p.FilePath)
	}
	if event == "Notification" {
		if err := store.RecordEvent(db, task.ID, "notification", strings.TrimSpace(p.NotificationType+" "+p.Message)); err != nil {
			return "", err
		}
		// The session is blocked on the human (a question or a permission prompt). The answer fires no
		// hook, so the task stays here until the next Stop (which resumes it) or prompt.
		if task.Status == store.StatusRunning && (p.NotificationType == "permission_prompt" || p.NotificationType == "elicitation_dialog") {
			_, err := store.UpdateTaskStatus(db, task.ID, store.StatusNeedsYou, store.ActorSystem)
			return "", err
		}
		return "", nil
	}
	if event != "UserPromptSubmit" && event != "Stop" {
		return "", nil
	}
	run, err := resolveRun(db, task, p.SessionID)
	if err != nil {
		return "", err
	}
	if event == "UserPromptSubmit" {
		return "", onPrompt(db, task, run)
	}
	if p.LastAssistantMessage == "" && run.Runner == "agy" {
		logPath := filepath.Join(home, "agy-logs", task.Key+".log")
		if b, err := os.ReadFile(logPath); err == nil {
			p.LastAssistantMessage = agy.LastTurnOutput(string(b))
		}
	}
	// Before onStop: its status change fires the chat DM, which reads pr_url for the Open PR button.
	prErr := refreshPR(db, task.ID)
	if prErr != nil { // best effort
		store.RecordEvent(db, task.ID, "error", "pr refresh: "+prErr.Error())
	}
	gaps, err := completionGaps(db, task, run, p, prErr == nil)
	if err != nil {
		store.RecordEvent(db, task.ID, "error", "completion check: "+err.Error())
	}
	if len(gaps) > 0 { // the session keeps going; its next Stop records the run
		if err := holdReply(db, run, p.LastAssistantMessage); err != nil {
			return "", err
		}
		return stopBlock(gaps), store.RecordEvent(db, task.ID, "completion_check", strings.Join(gaps, "; "))
	}
	return "", onStop(db, task, run, p)
}

// refreshPR syncs the task's PR state as its session returns; the dispatcher poll is the fallback.
func refreshPR(db *sql.DB, id int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t, err := store.GetTask(db, id) // re-read for the latest status
	if err != nil {
		return err
	}
	return gh.SyncTask(ctx, db, t)
}

// resolveRun finds the run for the session, adopts a launching one, or creates an interactive run.
func resolveRun(db *sql.DB, task store.Task, session string) (store.Run, error) {
	runs, err := store.ListRuns(db, task.ID)
	if err != nil {
		return store.Run{}, err
	}
	if session != "" {
		for i := len(runs) - 1; i >= 0; i-- { // resumes share a session id; newest wins
			if runs[i].SessionID != nil && *runs[i].SessionID == session {
				return runs[i], nil
			}
		}
	}
	var sp *string
	if session != "" {
		sp = &session
	}
	// A launching run has heard no hook yet, so its session id is at most a guess: a resume run starts
	// with the old id, but Claude may run the resume as a copy under a new one (docs/session-identity.md).
	for i := len(runs) - 1; i >= 0; i-- {
		if r := runs[i]; r.Status == "launching" {
			r.SessionID, r.Status = sp, "running" // running now, so onPrompt does not audit the start again
			auditStart(task.ID, r)
			return r, store.UpdateRun(db, r)
		}
	}
	// If this task has an active agy auto run, the hook belongs to it (DetectConversationID may have picked a different id).
	for i := len(runs) - 1; i >= 0; i-- {
		if r := runs[i]; (r.Status == "running" || r.Status == "launching") && r.Runner == "agy" && r.Kind == "auto" {
			if session != "" && (r.SessionID == nil || *r.SessionID != session) {
				r.SessionID = sp
				_ = store.UpdateRun(db, r)
			}
			return r, nil
		}
	}
	// Only Claude calls hooks, so a run they create is a Claude run.
	r, err := store.CreateRun(db, store.Run{TaskID: task.ID, Kind: "interactive", Mode: "fresh", Status: "running", SessionID: sp, Runner: "claude"})
	if err == nil {
		auditStart(task.ID, r)
	}
	return r, err
}

func auditStart(taskID int64, r store.Run) {
	store.Audit("epic.run.start", "task_id", taskID, "run", r.ID, "session", deref(r.SessionID))
}

// holdReply keeps the reply of a Stop the completion check blocked on the still-running run, so the
// run's final Stop posts it ahead of the fix-up message; often that blocked reply is the actual answer.
func holdReply(db *sql.DB, run store.Run, msg string) error {
	_, text, _ := statusblock.Parse(msg)
	if text = strings.TrimSpace(text); text == "" {
		return nil
	}
	if run.FinalMessage != nil && *run.FinalMessage != "" {
		text = *run.FinalMessage + "\n\n" + text
	}
	run.FinalMessage = &text
	return store.UpdateRun(db, run)
}

func onPrompt(db *sql.DB, task store.Task, run store.Run) error {
	if run.FinalMessage != nil { // a new prompt starts a new reply; drop text held from an earlier turn
		run.FinalMessage = nil
		if err := store.UpdateRun(db, run); err != nil {
			return err
		}
	}
	if run.Status != "running" {
		run.Status = "running"
		if err := store.UpdateRun(db, run); err != nil {
			return err
		}
		auditStart(task.ID, run)
	}
	if task.Status == store.StatusNeedsYou {
		if latest, err := store.LatestRun(db, task.ID); err == nil && latest.ID == run.ID {
			_, err = store.UpdateTaskStatus(db, task.ID, store.StatusRunning, store.ActorSystem)
			return err
		}
	}
	return nil
}

func onStop(db *sql.DB, task store.Task, run store.Run, p payload) error {
	return ProcessStop(db, task, run, p.LastAssistantMessage, p.TranscriptPath)
}

// ProcessStop processes session completion (from hook or background log extraction).
// It extracts the <conveyor> status block, updates the run, creates agent comments,
// records learnings/spawns, and transitions the task status accordingly.
func ProcessStop(db *sql.DB, task store.Task, run store.Run, lastAssistantMessage, transcriptPath string) error {
	block, final, hasBlock := statusblock.Parse(lastAssistantMessage)
	if !hasBlock {
		final = strings.TrimSpace(lastAssistantMessage)
	}
	// Still running with a final message: replies of Stops the completion check blocked (holdReply).
	if held := deref(run.FinalMessage); run.Status == "running" && held != "" {
		final = strings.TrimSpace(held + "\n\n" + final)
	}
	if transcriptPath != "" {
		if u, err := transcript.Sum(transcriptPath, run.Prompt); err == nil {
			run.TokensIn, run.TokensOut = int64(u.In), int64(u.Out)
			run.CacheRead, run.CacheWrite = int64(u.CacheRead), int64(u.CacheWrite)
			run.CtxPeak, run.APICalls = int64(u.CtxPeak), int64(u.Calls)
		} else {
			store.RecordEvent(db, task.ID, "error", "transcript usage: "+err.Error())
		}
		if w, err := transcript.CountWaste(transcriptPath, run.Prompt); err == nil {
			run.HookDenials, run.Rereads = int64(w.HookDenials), int64(w.Rereads)
			run.DupBash, run.BigResults = int64(w.DupBash), int64(w.BigResults)
		} // a read error is already recorded by Sum above
	}
	run.FinalMessage = &final
	ended := time.Now().UTC().Format(time.RFC3339)
	run.Status, run.EndedAt = "finished", &ended
	if hasBlock {
		run.BlockStatus, run.Summary, run.State, run.Next = &block.Status, &block.Summary, &block.State, &block.Next
		var sb strings.Builder
		for _, c := range block.Criteria {
			mark := " "
			if c.Met {
				mark = "x"
			}
			fmt.Fprintf(&sb, "- [%s] %s\n", mark, c.Text)
		}
		crit := strings.TrimSpace(sb.String())
		run.Criteria = &crit
	}
	if err := store.UpdateRun(db, run); err != nil {
		return err
	}
	store.Audit("epic.run.end", "task_id", task.ID, "run", run.ID, "session", deref(run.SessionID))
	if hasBlock {
		store.Audit("epic.status_block", "task_id", task.ID, "run", run.ID, "status", block.Status, "summary", block.Summary)
	}
	if run.Kind != "auto" {
		return settleInteractive(db, task)
	}

	body := final
	if body == "" && hasBlock {
		body = block.Summary
	}
	if body != "" {
		if _, err := store.PostRunComment(db, store.Comment{TaskID: task.ID, Author: store.AuthorAgent, Body: body, RunID: &run.ID}); err != nil {
			return err
		}
	}
	var repoID *int64
	for _, r := range task.Repos {
		if r.IsPrimary {
			id := r.RepoID
			repoID = &id
		}
	}
	for _, l := range block.Learnings {
		// Skip a learning this repo already has in any status (case/punctuation-insensitive), so a rejected one doesn't come back.
		if err := store.InsertLearningIfNew(db, task.ID, run.ID, repoID, l, ended); err != nil {
			return err
		}
	}
	core.ApplyDecisionLines(db, task.ID, block.Decisions, block.Disputes, block.Undisputes)
	for _, s := range block.Spawn {
		if err := store.RecordEvent(db, task.ID, "spawn", s); err != nil {
			return err
		}
	}
	if _, err := core.SpawnChildren(db, task, block.Spawn); err != nil {
		// A failed spawn must not leave the task stuck in running; record it and transition anyway.
		store.RecordEvent(db, task.ID, "spawn_failed", err.Error())
	}

	if task.Status == store.StatusNeedsYou { // parked by a question Notification; the answer let the run continue
		if _, err := store.UpdateTaskStatus(db, task.ID, store.StatusRunning, store.ActorSystem); err != nil {
			return err
		}
		task.Status = store.StatusRunning
	}
	if task.Status != store.StatusRunning {
		return nil // e.g. already moved by a human; nothing to transition
	}
	pending, err := store.UnconsumedComments(db, task.ID)
	if err != nil {
		return err
	}
	to, oldFails := store.StatusNeedsYou, task.ConsecutiveFailures
	switch {
	case len(pending) > 0:
		to = store.StatusTodo
	case !hasBlock && bgPending(transcriptPath, run.Prompt):
		return nil // waiting on its own background job; its completion wakes the session and Stops again
	case hasBlock && block.Status == "done":
		to = store.StatusReview
		task.ConsecutiveFailures = 0
		if gate, err := store.EpicAtGate(db, task); err != nil {
			return err
		} else if gate {
			to = store.StatusNeedsYou // charted: waits for `conveyor epic approve`
			store.Audit("epic.gate", "task_id", task.ID, "result", "waiting")
		} else if store.IsEpic(task) && store.EpicStage(task) == store.StageChart && !store.EpicGateOn(task) {
			if f, err := store.ActiveFlow(db, task.ID); err != nil {
				return err
			} else if f == nil { // gate off: charting is enough to start flow 1
				if _, err := store.ApproveEpic(db, task.ID); err != nil {
					return err
				}
			}
		}
	case hasBlock:
		task.ConsecutiveFailures++
		max, _ := strconv.Atoi(store.GetSetting(db, "max_consecutive_failures"))
		to = store.StatusTodo
		if task.ConsecutiveFailures >= max {
			to = store.StatusFailed
		}
	}
	if task.ConsecutiveFailures != oldFails {
		if err := store.UpdateTask(db, task); err != nil {
			return err
		}
	}
	_, err = store.UpdateTaskStatus(db, task.ID, to, store.ActorSystem)
	return err
}

// settleInteractive hands a running task back once an outside (manual) session goes idle, so
// messages queued meanwhile are not stuck behind a session the human may never close: queued
// comments requeue the task (todo), otherwise it waits on the human (needs_you), the same edge as
// reapInteractive. Another active run on the task keeps it running.
func settleInteractive(db *sql.DB, task store.Task) error {
	if cur, err := store.GetTask(db, task.ID); err != nil || cur.Status != store.StatusRunning {
		return err
	}
	active, err := store.ActiveRuns(db)
	if err != nil {
		return err
	}
	for _, o := range active {
		if o.TaskID == task.ID {
			return nil
		}
	}
	to := store.StatusNeedsYou
	if pending, err := store.UnconsumedComments(db, task.ID); err != nil {
		return err
	} else if len(pending) > 0 {
		to = store.StatusTodo
	}
	_, err = store.UpdateTaskStatus(db, task.ID, to, store.ActorSystem)
	return err
}

func bgPending(path, prompt string) bool {
	if path == "" {
		return false
	}
	ok, err := transcript.BackgroundPending(path, prompt, time.Now())
	return err == nil && ok
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
