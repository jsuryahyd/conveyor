package store

import (
	"database/sql"
	"fmt"
)

const runCols = `id, task_id, kind, mode, bg_id, session_id, status, prompt, block_status,
 summary, state, next, criteria, final_message, tokens_in, tokens_out, cache_read, cache_write,
 ctx_peak, api_calls, hook_denials, rereads, dup_bash, big_results, error, started_at, ended_at`

func scanRun(r interface{ Scan(...any) error }) (Run, error) {
	var x Run
	err := r.Scan(&x.ID, &x.TaskID, &x.Kind, &x.Mode, &x.BgID, &x.SessionID, &x.Status, &x.Prompt, &x.BlockStatus,
		&x.Summary, &x.State, &x.Next, &x.Criteria, &x.FinalMessage, &x.TokensIn, &x.TokensOut, &x.CacheRead, &x.CacheWrite,
		&x.CtxPeak, &x.APICalls, &x.HookDenials, &x.Rereads, &x.DupBash, &x.BigResults, &x.Error, &x.StartedAt, &x.EndedAt)
	return x, err
}

func queryRuns(db *sql.DB, where string, args ...any) ([]Run, error) {
	rows, err := db.Query(`SELECT `+runCols+` FROM runs WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("query runs: %w", err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateRun inserts a run; StartedAt defaults to now.
func CreateRun(db *sql.DB, r Run) (Run, error) {
	if r.StartedAt == "" {
		r.StartedAt = now()
	}
	res, err := db.Exec(`INSERT INTO runs (task_id, kind, mode, bg_id, session_id, status, prompt, started_at) VALUES (?,?,?,?,?,?,?,?)`,
		r.TaskID, r.Kind, r.Mode, r.BgID, r.SessionID, r.Status, r.Prompt, r.StartedAt)
	if err != nil {
		return Run{}, fmt.Errorf("create run: %w", err)
	}
	r.ID, _ = res.LastInsertId()
	return r, nil
}

// GetRun returns the run or a wrapped sql.ErrNoRows.
func GetRun(db *sql.DB, id int64) (Run, error) {
	r, err := scanRun(db.QueryRow(`SELECT `+runCols+` FROM runs WHERE id=?`, id))
	if err != nil {
		return Run{}, fmt.Errorf("run %d: %w", id, err)
	}
	return r, nil
}

// UpdateRun writes every mutable column of the run.
func UpdateRun(db *sql.DB, r Run) error {
	res, err := db.Exec(`UPDATE runs SET bg_id=?, session_id=?, status=?, prompt=?, block_status=?, summary=?, state=?, next=?,
 criteria=?, final_message=?, tokens_in=?, tokens_out=?, cache_read=?, cache_write=?, ctx_peak=?, api_calls=?,
 hook_denials=?, rereads=?, dup_bash=?, big_results=?, error=?, ended_at=? WHERE id=?`,
		r.BgID, r.SessionID, r.Status, r.Prompt, r.BlockStatus, r.Summary, r.State, r.Next,
		r.Criteria, r.FinalMessage, r.TokensIn, r.TokensOut, r.CacheRead, r.CacheWrite, r.CtxPeak, r.APICalls,
		r.HookDenials, r.Rereads, r.DupBash, r.BigResults, r.Error, r.EndedAt, r.ID)
	if err != nil {
		return fmt.Errorf("update run %d: %w", r.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("run %d: %w", r.ID, sql.ErrNoRows)
	}
	return nil
}

func oneRun(db *sql.DB, what string, where string, args ...any) (Run, error) {
	rs, err := queryRuns(db, where+` LIMIT 1`, args...)
	if err != nil {
		return Run{}, err
	}
	if len(rs) == 0 {
		return Run{}, fmt.Errorf("%s: %w", what, sql.ErrNoRows)
	}
	return rs[0], nil
}

// RunBySession finds a run by Claude session UUID.
func RunBySession(db *sql.DB, sessionID string) (Run, error) {
	return oneRun(db, "run for session "+sessionID, `session_id=? ORDER BY id DESC`, sessionID)
}

// LatestRun returns the task's newest run.
func LatestRun(db *sql.DB, taskID int64) (Run, error) {
	return oneRun(db, fmt.Sprintf("latest run of task %d", taskID), `task_id=? ORDER BY id DESC`, taskID)
}

// ListRuns returns a task's runs oldest first.
func ListRuns(db *sql.DB, taskID int64) ([]Run, error) {
	return queryRuns(db, `task_id=? ORDER BY id`, taskID)
}

// ActiveRuns returns runs that are launching or running.
func ActiveRuns(db *sql.DB) ([]Run, error) {
	return queryRuns(db, `status IN ('launching','running') ORDER BY id`)
}

// ClearTaskSessionIDs sets session_id to NULL on all runs of a task so future launches start fresh.
func ClearTaskSessionIDs(db *sql.DB, taskID int64) error {
	_, err := db.Exec(`UPDATE runs SET session_id = NULL WHERE task_id = ?`, taskID)
	return err
}
