// Package hook turns Claude Code hook events into store updates (tech-spec §8).
package hook

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JayaSuryaOolio/conveyor/internal/core"
	"github.com/JayaSuryaOolio/conveyor/internal/statusblock"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
	"github.com/JayaSuryaOolio/conveyor/internal/transcript"
	"github.com/JayaSuryaOolio/conveyor/internal/workspace"
)

type payload struct {
	SessionID            string `json:"session_id"`
	ConversationID       string `json:"conversation_id"` // agy alias
	Cwd                  string `json:"cwd"`
	TaskKey              string `json:"task_key"` // optional direct key
	TranscriptPath       string `json:"transcript_path"`
	LastAssistantMessage string `json:"last_assistant_message"`
	Message              string `json:"message"`
	NotificationType     string `json:"notification_type"`
}

// Handle processes one hook event. Callers log the returned error and still exit 0.
func Handle(db *sql.DB, home, event string, raw []byte) error {
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("parse %s payload: %w", event, err)
	}
	if p.SessionID == "" && p.ConversationID != "" {
		p.SessionID = p.ConversationID
	}
	key := workspace.KeyFromCwd(home, p.Cwd)
	if key == "" && p.TaskKey != "" {
		key = p.TaskKey
	}
	if key == "" {
		return nil // not one of ours
	}
	task, err := store.GetTaskByKey(db, key)
	if err != nil {
		return fmt.Errorf("hook %s: %w", event, err)
	}
	if event == "Notification" {
		return store.RecordEvent(db, task.ID, "notification", strings.TrimSpace(p.NotificationType+" "+p.Message))
	}
	if event != "UserPromptSubmit" && event != "Stop" {
		return nil
	}
	run, err := resolveRun(db, task, p.SessionID)
	if err != nil {
		return err
	}
	if event == "UserPromptSubmit" {
		return onPrompt(db, task, run)
	}
	return onStop(db, task, run, p)
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
	for i := len(runs) - 1; i >= 0; i-- {
		if r := runs[i]; r.SessionID == nil && r.Status == "launching" {
			r.SessionID = sp
			return r, store.UpdateRun(db, r)
		}
	}
	return store.CreateRun(db, store.Run{TaskID: task.ID, Kind: "interactive", Mode: "fresh", Status: "running", SessionID: sp})
}

func onPrompt(db *sql.DB, task store.Task, run store.Run) error {
	if run.Status != "running" {
		run.Status = "running"
		if err := store.UpdateRun(db, run); err != nil {
			return err
		}
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
	if transcriptPath != "" {
		if u, err := transcript.Sum(transcriptPath, run.Prompt); err == nil {
			run.TokensIn, run.TokensOut = int64(u.In), int64(u.Out)
			run.CacheRead, run.CacheWrite = int64(u.CacheRead), int64(u.CacheWrite)
			run.CtxPeak, run.APICalls = int64(u.CtxPeak), int64(u.Calls)
		} else {
			store.RecordEvent(db, task.ID, "error", "transcript usage: "+err.Error())
		}
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
	if run.Kind != "auto" {
		return nil
	}

	body := final
	if body == "" && hasBlock {
		body = block.Summary
	}
	if body != "" {
		if _, err := store.AddComment(db, store.Comment{TaskID: task.ID, Author: store.AuthorAgent, Body: body, RunID: &run.ID}); err != nil {
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
		if _, err := db.Exec(`INSERT INTO learnings (task_id, run_id, repo_id, body, status, created_at) VALUES (?,?,?,?,'proposed',?)`,
			task.ID, run.ID, repoID, l, ended); err != nil {
			return fmt.Errorf("insert learning: %w", err)
		}
	}
	for _, s := range block.Spawn {
		if err := store.RecordEvent(db, task.ID, "spawn", s); err != nil {
			return err
		}
	}
	if _, err := core.SpawnChildren(db, task, block.Spawn); err != nil {
		// A failed spawn must not leave the task stuck in running; record it and transition anyway.
		store.RecordEvent(db, task.ID, "spawn_failed", err.Error())
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
	case hasBlock && block.Status == "done":
		to = store.StatusReview
		task.ConsecutiveFailures = 0
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
